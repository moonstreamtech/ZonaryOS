# Deploying ZonaryOS (near-term dev/test target only)

This document is the command-by-command handoff for deploying the containerized stack (`docker-compose.yml`) to the Oracle Cloud Always Free instance from `docs/OPEN_POINTS.md` item 34. **Not a production runbook** - see the caveats in `docs/DEVELOPMENT.md`'s "Deploying to Oracle Cloud Always Free" section (no SLA, no backup, single instance, Oracle can reclaim capacity). This document only covers making that dev/test instance reachable at `https://zonaryos.duckdns.org` instead of `localhost`.

Written for a human running each command over their own SSH session and pasting output back for review - each step says what success looks like so a mismatch is obvious before moving to the next step. **Direct SSH/deployment execution was not possible from the agent session that authored this** (its network egress is HTTPS-only, no raw TCP/port 22 - see the session history); everything below is designed and locally verified (see "How this was verified" at the end) but not yet run against the real instance.

## Why this isn't just `docker compose up -d`

`docker-compose.yml` uses `network_mode: host` deliberately: Keycloak (in `start-dev` mode) stamps each issued token's `iss` claim from whatever host:port the request that minted it used, and the frontend's login/callback routes use one `ZONARYOS_KEYCLOAK_ISSUER_URL` env var both to build the browser-facing redirect *and* for the frontend container's own server-side token exchange. Locally, `localhost` satisfies both. In a real deployment, the actual browser is a remote user's machine - it has to reach Keycloak directly too, not just the frontend.

The fix (`docker-compose.prod.yml`, `deploy/nginx/zonaryos.conf`): nginx reverse-proxies **both** the frontend (`/`) and Keycloak (`/auth/`) under one public hostname, `zonaryos.duckdns.org`. Keycloak is told its own external identity explicitly via `KC_HOSTNAME=https://zonaryos.duckdns.org/auth` (Keycloak 26's "hostname:v2" provider accepts a full URL, path prefix included) instead of deriving it per-request, so the issuer baked into every token is identical regardless of whether a request arrived through nginx (the real browser path) or hit `localhost` directly (backend/frontend server-side calls) - those two paths must produce byte-identical issuer values or `internal/identity.NewVerifier`'s discovery/issuer check rejects the token. `KC_HTTP_RELATIVE_PATH=/auth` keeps Keycloak's own internally-generated links (its login theme, redirects) consistent with the external `/auth/` path nginx exposes, so nothing needs path-rewriting in nginx. The backend and frontend containers get an `extra_hosts` entry mapping `zonaryos.duckdns.org` to `127.0.0.1`, so their own outbound calls to the public issuer URL loop back through nginx on the same host rather than depending on the instance's network correctly hairpinning a request back to its own public IP.

The backend (8080) and Postgres (5432) are **not** proxied by nginx at all and must never be reachable from the public internet - the frontend calls the backend directly over its own `localhost`, and nothing else needs to reach either.

## Prerequisites already satisfied

- The `zonaryos_deploy_ed25519` SSH keypair was generated in a prior session and its public key added to `~/.ssh/authorized_keys` for the `zonaryos` user on the instance (144.24.254.120 / `zonaryos.duckdns.org`).
- `zonaryos.duckdns.org` already resolves to the instance's public IP.
- The `zonaryos` user has Docker access and exactly three sudo commands: `nginx -t`, `systemctl reload nginx`, `certbot`. Nothing below assumes broader sudo - if a step seems to need it, stop and ask rather than improvising.
- The instance is shared with other live sites (knkznbot, kozmestest, kozmes, makulio, mehmetcik-kutuphane, miracity) behind the same nginx. **Only `deploy/nginx/zonaryos.conf` is ZonaryOS's to add or edit** - no other `/etc/nginx/sites-enabled/*` file is touched by any step below.

## Command sequence

Run each step from an SSH session as `zonaryos@144.24.254.120`. Paste the actual output back before moving to the next step if anything looks different from what's noted.

**1. Connect and orient (read-only).**

```
ssh zonaryos@144.24.254.120
docker ps
ls -la /etc/nginx/sites-enabled/
cat /etc/nginx/sites-enabled/*   # read only - to match existing style, don't copy verbatim
```
Expect: `docker ps` returns a table (possibly empty if nothing's running yet, but no "permission denied" or "cannot connect" error). The `sites-enabled` listing shows the other live sites' config files. **If the style here differs meaningfully from `deploy/nginx/zonaryos.conf` below (e.g. this box's other sites use a different proxy_pass header convention), adjust `deploy/nginx/zonaryos.conf` to match before step 6 rather than dropping in a stylistically inconsistent file.**

**2. Get the repository onto the instance.**

```
git clone https://github.com/moonstreamtech/ZonaryOS.git ~/zonaryos-app
cd ~/zonaryos-app
git checkout claude/oracle-containerization   # or main, once this branch is merged
```
Expect: a normal clone; `git log -1` shows this work's commit.

**3. Build the images natively on the instance (arm64 - avoids cross-compilation entirely).**

```
docker compose build
```
Expect: three image builds (`migrate`/`backend` share one image, plus `frontend`) completing with no error. This will take several minutes on Always Free's modest CPU allocation - that's normal, not a hang.

**4. Bring up the full stack with the production overlay.**

```
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
docker compose -f docker-compose.yml -f docker-compose.prod.yml ps
```
Expect: `postgres`, `keycloak`, `backend`, `frontend` all `Up` (`migrate` shows `Exited (0)` - it's a one-shot job, not a bug). If `backend` shows repeated restarts, give it ~30s - `restart: on-failure:10` is expected to fire once or twice while Keycloak finishes booting (documented in `docs/DEVELOPMENT.md`).

**5. Confirm the stack works internally before exposing it publicly.**

```
BACKEND_URL=http://localhost:8080 FRONTEND_URL=http://localhost:3000 \
KEYCLOAK_ISSUER_URL=http://localhost:8081/auth/realms/zonaryos \
./scripts/e2e_smoke_test.sh
```
Expect: ends with `E2E SMOKE TEST PASSED: login + wizard -> firm creation -> add stock -> sell, all against a real stack`. Note the issuer URL here now includes `/auth` - this is `docker-compose.prod.yml`'s `KC_HTTP_RELATIVE_PATH` taking effect even for the direct-localhost path, and is expected.

**6. Place the ZonaryOS nginx config (does not touch any other site).**

```
sudo cp ~/zonaryos-app/deploy/nginx/zonaryos.conf /etc/nginx/sites-available/zonaryos.conf
sudo ln -s /etc/nginx/sites-available/zonaryos.conf /etc/nginx/sites-enabled/zonaryos.conf
sudo nginx -t
```
Expect: `nginx: configuration file /etc/nginx/nginx.conf test is successful`. **If this fails, fix `zonaryos.conf` only** (re-copy a corrected version and re-test) - never edit any other file in `sites-enabled` to make the test pass.

```
sudo systemctl reload nginx
```
Expect: no output (success) or a benign confirmation; `systemctl status nginx` should still show `active (running)`.

**7. Verify the plain-HTTP path works before requesting a certificate.**

```
curl -s -o /dev/null -w "%{http_code}\n" http://zonaryos.duckdns.org/
curl -s http://zonaryos.duckdns.org/auth/realms/zonaryos/.well-known/openid-configuration | head -c 300
```
Expect: the first command prints `200` (or a redirect code if the frontend itself redirects unauthenticated requests - either is fine as long as it's not `502`/`504`/connection refused). The second prints a JSON fragment starting `{"issuer":"https://zonaryos.duckdns.org/auth/realms/zonaryos",...` - **the issuer must read `https://zonaryos.duckdns.org/auth/realms/zonaryos` exactly**; anything else (e.g. `http://localhost:8081/...`) means `KC_HOSTNAME` in `docker-compose.prod.yml` wasn't picked up and login will break.

**8. Get a real TLS certificate for this domain only.**

```
sudo certbot --nginx -d zonaryos.duckdns.org
```
Expect: certbot asks for an email (for renewal notices) and agreement to the ToS, obtains the certificate via the HTTP-01 challenge, and reports something like `Successfully deployed certificate for zonaryos.duckdns.org`. It edits `/etc/nginx/sites-available/zonaryos.conf` in place (adding a `443 ssl` server block and an HTTP->HTTPS redirect on port 80) - **this is the one expected, intentional edit to that file after step 6**; it does not touch any other domain's certificate or config, since `-d` scopes it to exactly `zonaryos.duckdns.org`.

```
sudo nginx -t && sudo systemctl reload nginx
```
Expect: same success output as step 6 (certbot's own `--nginx` flow usually reloads automatically, but confirm explicitly).

**9. Verify from outside the instance - the real proof.**

From your own machine (not the server):
```
curl -s -o /dev/null -w "%{http_code}\n" https://zonaryos.duckdns.org/
curl -s https://zonaryos.duckdns.org/auth/realms/zonaryos/.well-known/openid-configuration | head -c 300
```
Expect: `200`, and the same `"issuer":"https://zonaryos.duckdns.org/auth/realms/zonaryos"` as step 7, now over real HTTPS with a browser-trusted certificate (no `-k`/`--insecure` needed).

Then, in a real browser: visit `https://zonaryos.duckdns.org`, trigger login, sign in as `dev@zonaryos.local` / `zonaryos-dev` (the same dev user `deploy/keycloak/zonaryos-realm.json` already seeds), and confirm it lands back on the ZonaryOS homepage logged in. Then walk wizard -> create a firm -> add stock -> sell once through the UI to confirm the full path, not just login.

**10. Confirm nothing else on the box broke.**

```
curl -s -o /dev/null -w "%{http_code}\n" https://<one-other-site-domain>/
curl -s -o /dev/null -w "%{http_code}\n" https://<another-other-site-domain>/
```
Expect: both return the same status code they returned before step 6 (this is a spot check, not exhaustive) - confirming the nginx reload in steps 6/8 didn't disturb any other `sites-enabled` file.

## Firewall: Oracle Cloud Security List (manual step, not part of this repo)

This is an OCI console/API change, not a command this sequence can run - flagging it explicitly rather than assuming it's done.

**Must be open to `0.0.0.0/0`:**
- `443/tcp` - the only public entry point (nginx -> frontend/Keycloak)
- `80/tcp` - needed for certbot's HTTP-01 challenge (step 8) and any future cert renewal

**Must stay closed to the public internet** (reachable only via `localhost` from other processes on the same instance, per `network_mode: host`'s tradeoff documented in `docker-compose.yml`):
- `5432/tcp` - Postgres. No other service on this box should ever need it, and nothing here proxies it.
- `8080/tcp` - backend. The frontend reaches it over its own `localhost`; nginx never proxies it (see `deploy/nginx/zonaryos.conf`).
- `8081/tcp` - Keycloak's raw port. Only reachable through nginx's `/auth/` path (443/80), never directly.

If the Security List currently has a broader rule (e.g. `0.0.0.0/0` on all ports, or a leftover rule from before this containerized stack existed), narrow it to just 443/80 rather than adding to it - but since this is a shared box with other live services, **confirm with the Developer before changing or removing any existing Security List rule that isn't obviously ZonaryOS's own**, in case another service on the same instance depends on it.

## How this was verified

Before handoff, `docker-compose.prod.yml` + `deploy/nginx/zonaryos.conf` were proven together in a local sandbox: Postgres, the `migrate` job, the real `backend` image, and the real `frontend` image (all built from this repo's actual `Dockerfile`/`web/Dockerfile`, unmodified) were brought up with the production overlay, fronted by an nginx instance replicating the exact path-based routing above (`/` -> frontend, `/auth/` -> a Keycloak stand-in - see next paragraph, both over a locally-trusted TLS certificate standing in for the real Let's Encrypt one certbot issues in step 8), with `zonaryos.duckdns.org` resolved to loopback for the test.

**One deviation from a fully real proof, disclosed rather than silently worked around**: the sandbox this was verified in has its egress restricted by policy, and `quay.io` (Keycloak's image registry) is blocked for this session (confirmed via the proxy's own status endpoint, not assumed) - the real Keycloak image could not be pulled. In its place, a minimal local script stood in for Keycloak's OIDC surface (discovery document with a fixed `issuer`, the authorization endpoint's login form, and a token endpoint that performs real PKCE `code_verifier`/`code_challenge` validation) - not a security-complete OIDC implementation, and not part of this handoff, but enough to drive the actual thing in question through the actual frontend code: a real headless-browser PKCE round-trip (`/api/auth/login` -> redirect to the `/auth/realms/zonaryos/protocol/openid-connect/auth` path through nginx -> credentials submitted -> redirect back to `/api/auth/callback` -> a real server-side token-exchange `fetch` from inside the frontend container, over HTTPS, through nginx, resolved via `extra_hosts` -> `zonaryos_session` cookie set -> homepage re-fetched and confirmed `200`). Separately, the real `backend` container's own `identity.NewVerifier` (Go, `internal/identity`) successfully discovered OIDC configuration from `https://zonaryos.duckdns.org/auth/realms/zonaryos` through the same nginx path at container startup with no errors - proving the `KC_HOSTNAME`/`extra_hosts`/nginx combination resolves a real Go HTTP client's discovery call correctly, independent of the stand-in.

**What this does and doesn't prove**: the reverse-proxy topology, the fixed-issuer design, the `extra_hosts` loopback mechanism, and the frontend's actual login/callback code all worked end-to-end against something that speaks the same OIDC surface Keycloak does. It does **not** prove Keycloak 26 itself honors `KC_HOSTNAME`/`KC_HTTP_RELATIVE_PATH`/`KC_PROXY_HEADERS` exactly as documented here - that column is real, run against the real image, for the first time at step 5 (against `localhost` directly, before nginx is even involved) and step 9 (against the public URL) of the command sequence above. If step 5's discovery-document issuer doesn't read `https://zonaryos.duckdns.org/auth/realms/zonaryos` exactly, stop there rather than continuing to steps 6-9 and report back what it actually said.

What else could not be verified locally: the real instance's Docker/OS environment, the real `certbot`/Let's Encrypt issuance, the existing nginx site files' actual style (this session has no SSH access to read them - see step 1), and the other five sites' actual behavior after a reload.
