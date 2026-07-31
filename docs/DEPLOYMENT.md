# Deploying ZonaryOS (near-term dev/test target only)

This document is the command-by-command handoff for deploying the containerized stack (`docker-compose.yml`) to the Oracle Cloud Always Free instance from `docs/OPEN_POINTS.md` item 34. **Not a production runbook** - see the caveats in `docs/DEVELOPMENT.md`'s "Deploying to Oracle Cloud Always Free" section (no SLA, no backup, single instance, Oracle can reclaim capacity). This document only covers making that dev/test instance reachable at `https://zonaryos.duckdns.org` instead of `localhost`.

Written for a human running each command over their own SSH session and pasting output back for review - each step says what success looks like so a mismatch is obvious before moving to the next step. **Direct SSH/deployment execution was not possible from the agent session that authored this** (its network egress is HTTPS-only, no raw TCP/port 22 - see the session history); everything below is designed and locally verified (see "How this was verified" at the end) but not yet run against the real instance.

## Why this isn't just `docker compose up -d`

`docker-compose.yml` uses `network_mode: host` deliberately: Keycloak (in `start-dev` mode) stamps each issued token's `iss` claim from whatever host:port the request that minted it used, and the frontend's login/callback routes use one `ZONARYOS_KEYCLOAK_ISSUER_URL` env var both to build the browser-facing redirect *and* for the frontend container's own server-side token exchange. Locally, `localhost` satisfies both. In a real deployment, the actual browser is a remote user's machine - it has to reach Keycloak directly too, not just the frontend.

The fix (`docker-compose.prod.yml`, `deploy/nginx/zonaryos.conf`): nginx reverse-proxies **both** the frontend (`/`) and Keycloak (`/auth/`) under one public hostname, `zonaryos.duckdns.org`. Keycloak is told its own external identity explicitly via `KC_HOSTNAME=https://zonaryos.duckdns.org/auth` (Keycloak 26's "hostname:v2" provider accepts a full URL, path prefix included) instead of deriving it per-request, so the issuer baked into every token is identical regardless of whether a request arrived through nginx (the real browser path) or hit `localhost` directly (backend/frontend server-side calls) - those two paths must produce byte-identical issuer values or `internal/identity.NewVerifier`'s discovery/issuer check rejects the token. `KC_HTTP_RELATIVE_PATH=/auth` keeps Keycloak's own internally-generated links (its login theme, redirects) consistent with the external `/auth/` path nginx exposes, so nothing needs path-rewriting in nginx. The backend and frontend containers get an `extra_hosts` entry mapping `zonaryos.duckdns.org` to `127.0.0.1`, so their own outbound calls to the public issuer URL loop back through nginx on the same host rather than depending on the instance's network correctly hairpinning a request back to its own public IP.

The backend (8080) and Postgres (5433 - see the note below on why not 5432) are **not** proxied by nginx at all and must never be reachable from the public internet - the frontend calls the backend directly over its own `localhost`, and nothing else needs to reach either.

**Postgres listens on 5433, not the default 5432** - a real collision found on the actual instance, not a hypothetical one: this box already runs a native (non-Docker) PostgreSQL via systemd, bound to `5432`. Since every service here uses `network_mode: host`, Docker's `ports:` publishing doesn't apply - there's no bridge network to remap - so the *server process itself* has to be told to listen elsewhere, which `docker-compose.yml`'s `postgres` service now does via `command: ["postgres", "-p", "5433"]`. **Don't assume 5432 (or 5433) is free on a future deploy target either** - `ss -tln` before deploying is what caught this the first time.

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
sudo nginx -T | grep -B5 'sites-enabled'
```
Expect: `docker ps` returns a table (possibly empty if nothing's running yet, but no "permission denied" or "cannot connect" error). The `sites-enabled` listing shows the other live sites' config files. **If the style here differs meaningfully from `deploy/nginx/zonaryos.conf` below (e.g. this box's other sites use a different proxy_pass header convention), adjust `deploy/nginx/zonaryos.conf` to match before step 6 rather than dropping in a stylistically inconsistent file.**

The last command (`nginx -T`) confirms an assumption `deploy/nginx/zonaryos.conf`'s rate-limiting section depends on: that `include /etc/nginx/sites-enabled/*;` sits directly inside the shared `nginx.conf`'s `http{}` block (the standard Debian/Ubuntu nginx package layout), so a `limit_req_zone` directive placed at `zonaryos.conf`'s own top level (outside its `server{}` block) is parsed in `http{}` context without touching `nginx.conf` or any other site's file. Expect the grep output to show `sites-enabled` referenced from inside an `http { ... }` block. **If it isn't** (e.g. this box uses a non-standard layout, or `sites-enabled` is included from somewhere else), stop before step 6 and ask rather than assuming the rate-limit zone will actually take effect - `nginx -t` alone won't catch a zone that's silently in the wrong context in every nginx version.

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
Expect: `nginx: configuration file /etc/nginx/nginx.conf test is successful`. **If this fails, fix `zonaryos.conf` only** (re-copy a corrected version and re-test) - never edit any other file in `sites-enabled` to make the test pass. If it fails specifically on the `limit_req_zone` line, that's the step-1 assumption about `sites-enabled` living inside `http{}` turning out false - stop and ask rather than moving the zone into `nginx.conf` or a `conf.d` file yourself.

```
sudo systemctl reload nginx
```
Expect: no output (success) or a benign confirmation; `systemctl status nginx` should still show `active (running)`.

**7. Verify the plain-HTTP path works before requesting a certificate.**

```
curl -s -o /dev/null -w "%{http_code}\n" http://zonaryos.duckdns.org/
curl -s http://zonaryos.duckdns.org/auth/realms/zonaryos/.well-known/openid-configuration | head -c 300
curl -sI http://zonaryos.duckdns.org/ | grep -i -E 'strict-transport-security|x-content-type-options|x-frame-options|content-security-policy'
```
Expect: the first command prints `200` (or a redirect code if the frontend itself redirects unauthenticated requests - either is fine as long as it's not `502`/`504`/connection refused). The second prints a JSON fragment starting `{"issuer":"https://zonaryos.duckdns.org/auth/realms/zonaryos",...` - **the issuer must read `https://zonaryos.duckdns.org/auth/realms/zonaryos` exactly**; anything else (e.g. `http://localhost:8081/...`) means `KC_HOSTNAME` in `docker-compose.prod.yml` wasn't picked up and login will break. The third prints all four security headers `deploy/nginx/zonaryos.conf` now adds (`Strict-Transport-Security`, `X-Content-Type-Options`, `X-Frame-Options`, `Content-Security-Policy`) - if any are missing, re-check `nginx -t` output and that the reload in step 6 actually picked up the new file.

Also worth a quick manual check here: try a rapid burst of requests against `/auth/` (e.g. `for i in $(seq 1 40); do curl -s -o /dev/null -w "%{http_code} " http://zonaryos.duckdns.org/auth/realms/zonaryos/.well-known/openid-configuration; done; echo`) and confirm some requests start returning `503` partway through - that's `limit_req zone=zonaryos_auth` (10 requests/sec/IP, burst 20) actually engaging. A burst that never produces a single `503` likely means the zone isn't in effect (see the step-1/step-6 caveats above).

**8. Get a real TLS certificate for this domain only.**

```
sudo certbot --nginx -d zonaryos.duckdns.org
```
Expect: certbot asks for an email (for renewal notices) and agreement to the ToS, obtains the certificate via the HTTP-01 challenge, and reports something like `Successfully deployed certificate for zonaryos.duckdns.org`. It edits `/etc/nginx/sites-available/zonaryos.conf` in place (adding a `443 ssl` server block and an HTTP->HTTPS redirect on port 80) - **this is the one expected, intentional edit to that file after step 6**; it does not touch any other domain's certificate or config, since `-d` scopes it to exactly `zonaryos.duckdns.org`.

Certbot's `--nginx` plugin clones the *entire* matched `server{}` block into the new `443` block, so the `add_header` lines added in this batch (sitting at `server{}` level, not duplicated per-`location`) should appear in both the port-80 (now redirect-only) and port-443 blocks afterward - confirm with `sudo cat /etc/nginx/sites-available/zonaryos.conf | grep -A2 'listen 443'` before moving on. **If the four `add_header` lines are missing from the new `443` block**, certbot's cloning behavior differs from what's documented above for this certbot version - re-add them to the `443` block by hand and note the discrepancy back in `deploy/nginx/zonaryos.conf`'s own comment for next time, rather than silently leaving HTTPS traffic without the headers.

```
sudo nginx -t && sudo systemctl reload nginx
```
Expect: same success output as step 6 (certbot's own `--nginx` flow usually reloads automatically, but confirm explicitly).

**9. Verify from outside the instance - the real proof.**

From your own machine (not the server):
```
curl -s -o /dev/null -w "%{http_code}\n" https://zonaryos.duckdns.org/
curl -s https://zonaryos.duckdns.org/auth/realms/zonaryos/.well-known/openid-configuration | head -c 300
curl -sI https://zonaryos.duckdns.org/ | grep -i -E 'strict-transport-security|x-content-type-options|x-frame-options|content-security-policy'
```
Expect: `200`, and the same `"issuer":"https://zonaryos.duckdns.org/auth/realms/zonaryos"` as step 7, now over real HTTPS with a browser-trusted certificate (no `-k`/`--insecure` needed). The third command should show the same four security headers as step 7's equivalent check, confirmed this time on the real HTTPS-serving block certbot created - this is the step that actually proves the "survives certbot's edit" claim in `deploy/nginx/zonaryos.conf`'s own comments, not just a reading of the file.

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
- `5433/tcp` - ZonaryOS's own Postgres (remapped from the default 5432 - see above - because this instance already runs a separate, native PostgreSQL on 5432 for other services on the box; that pre-existing instance's own firewall exposure is unrelated to this deployment and out of scope here). No other service on this box should ever need 5433, and nothing here proxies it.
- `8080/tcp` - backend. The frontend reaches it over its own `localhost`; nginx never proxies it (see `deploy/nginx/zonaryos.conf`).
- `8081/tcp` - Keycloak's raw port. Only reachable through nginx's `/auth/` path (443/80), never directly.

**Re-audited (not just re-asserted) after everything added on this branch since this section was first written** - the platform-admin firm list (`GET /api/platform-admin/firms`), the workflow-instance-counts endpoint (`GET /api/firms/{firmID}/workflow-instance-counts`), global search, and quick-create. All of these register on the backend's single `http.ServeMux` in `cmd/server/main.go` (`platformadmin.RegisterRoutes`, `workflow.RegisterRoutes`, etc., all called on the same `mux` bound to the same `ZONARYOS_HTTP_ADDR`/`:8080`) alongside every pre-existing backend route - none of them opens a new listener, a new port, or a separate process. The frontend pages that use them (`web/src/app/[locale]/platform-admin`, `.../search`, etc.) are ordinary routes inside the same Next.js app already served on `:3000`. **Conclusion: nothing new needs a port opened.** The Security List stays exactly as documented above - `443`/`80` public, `5433`/`8080`/`8081` closed.

If the Security List currently has a broader rule (e.g. `0.0.0.0/0` on all ports, or a leftover rule from before this containerized stack existed), narrow it to just 443/80 rather than adding to it - but since this is a shared box with other live services, **confirm with the Developer before changing or removing any existing Security List rule that isn't obviously ZonaryOS's own**, in case another service on the same instance depends on it.

## Automated Postgres backups (manual setup, once)

`deploy/backup/backup-postgres.sh` (script header has the full rationale) `pg_dump`s ZonaryOS's own Postgres via `docker compose exec postgres pg_dump` - not a raw `docker exec <container-name>`, so it doesn't depend on guessing the Compose-generated container name - gzips the result, and writes it to a plain host directory that Docker does not manage at all (`~/zonaryos-backups` by default), specifically so that a `docker compose down -v` (which deletes the named `zonaryos-postgres-data` volume) can never take out the backups in the same command as the live data. Rotation keeps the newest 7 dumps and deletes older ones.

**Provisional default, not a decided policy**: `docs/OPEN_POINTS.md` item 25 ("Backup Frequency / SLA Target") is still open. Daily cadence and 7-dump local retention here are placeholders so *something* runs before that item is resolved - not the Developer's final answer. Revisit both the cron cadence below and `RETENTION_COUNT` in the script once item 25 is decided.

**Out of scope, flagged as a follow-up, not built**: no offsite/cloud copy (S3 or equivalent) of these dumps exists anywhere. Everything stays on the single Oracle instance's local disk - see the "Interim decision" caveats already on `docs/OPEN_POINTS.md` item 34 (no SLA, single instance, Oracle can reclaim capacity) for why that single-instance risk was already known, not newly introduced by this backup script.

**1. Get the backup script and a target directory in place.**

```
mkdir -p ~/zonaryos-backups
chmod +x ~/zonaryos-app/deploy/backup/backup-postgres.sh
```
Expect: no output; `ls -ld ~/zonaryos-backups` shows a directory owned by the `zonaryos` user, outside `~/zonaryos-app` and outside anything Docker manages (not under `/var/lib/docker`).

**2. Run it once by hand before automating it.**

```
cd ~/zonaryos-app
./deploy/backup/backup-postgres.sh
ls -la ~/zonaryos-backups
```
Expect: log lines ending in `backup script finished successfully (...)`, and exactly one new file `~/zonaryos-backups/zonaryos-postgres-<UTC timestamp>.sql.gz`. Sanity-check it's a real dump, not an empty/failed one:
```
zcat ~/zonaryos-backups/zonaryos-postgres-*.sql.gz | head -c 200
```
Expect: output starting with a `pg_dump`-style header comment (`-- PostgreSQL database dump`).

**3. Schedule it daily via cron** (plain cron, not a systemd timer - matches this repo's existing convention of no systemd units under `deploy/` yet, and is the narrower change for a single scheduled job on a shared box).

```
crontab -l 2>/dev/null; echo "0 3 * * * cd \$HOME/zonaryos-app && ./deploy/backup/backup-postgres.sh >> \$HOME/zonaryos-backups/backup.log 2>&1" | crontab -
crontab -l
```
Expect: the final `crontab -l` shows the new line (daily at 03:00 server time - adjust the `0 3` if a different time is wanted, this is not a meaningful choice, just "sometime low-traffic"). Nothing here overwrites any pre-existing crontab entries for the `zonaryos` user - the first `crontab -l` in the pipeline is there so a mismatch (e.g. an existing crontab this command would otherwise silently replace) is visible before it's overwritten; if `crontab -l` shows anything unexpected, edit with `crontab -e` and add the line manually instead of piping.

**4. Confirm the cron entry actually runs** (don't just trust that it's scheduled - wait for, or force, one real firing).

```
run-parts --test /etc/cron.d 2>/dev/null; systemctl status cron 2>&1 | head -5
```
Expect: `cron`/`crond` shows `active (running)`. The real proof is the next day's `~/zonaryos-backups/backup.log` and a new dump file appearing without anyone running the script by hand - check back after the first scheduled firing.

## How this was verified

Before handoff, `docker-compose.prod.yml` + `deploy/nginx/zonaryos.conf` were proven together in a local sandbox: Postgres, the `migrate` job, the real `backend` image, and the real `frontend` image (all built from this repo's actual `Dockerfile`/`web/Dockerfile`, unmodified) were brought up with the production overlay, fronted by an nginx instance replicating the exact path-based routing above (`/` -> frontend, `/auth/` -> a Keycloak stand-in - see next paragraph, both over a locally-trusted TLS certificate standing in for the real Let's Encrypt one certbot issues in step 8), with `zonaryos.duckdns.org` resolved to loopback for the test.

**One deviation from a fully real proof, disclosed rather than silently worked around**: the sandbox this was verified in has its egress restricted by policy, and `quay.io` (Keycloak's image registry) is blocked for this session (confirmed via the proxy's own status endpoint, not assumed) - the real Keycloak image could not be pulled. In its place, a minimal local script stood in for Keycloak's OIDC surface (discovery document with a fixed `issuer`, the authorization endpoint's login form, and a token endpoint that performs real PKCE `code_verifier`/`code_challenge` validation) - not a security-complete OIDC implementation, and not part of this handoff, but enough to drive the actual thing in question through the actual frontend code: a real headless-browser PKCE round-trip (`/api/auth/login` -> redirect to the `/auth/realms/zonaryos/protocol/openid-connect/auth` path through nginx -> credentials submitted -> redirect back to `/api/auth/callback` -> a real server-side token-exchange `fetch` from inside the frontend container, over HTTPS, through nginx, resolved via `extra_hosts` -> `zonaryos_session` cookie set -> homepage re-fetched and confirmed `200`). Separately, the real `backend` container's own `identity.NewVerifier` (Go, `internal/identity`) successfully discovered OIDC configuration from `https://zonaryos.duckdns.org/auth/realms/zonaryos` through the same nginx path at container startup with no errors - proving the `KC_HOSTNAME`/`extra_hosts`/nginx combination resolves a real Go HTTP client's discovery call correctly, independent of the stand-in.

**What this does and doesn't prove**: the reverse-proxy topology, the fixed-issuer design, the `extra_hosts` loopback mechanism, and the frontend's actual login/callback code all worked end-to-end against something that speaks the same OIDC surface Keycloak does - confirmed both via a real headless-browser round-trip (Playwright/Chromium) landing back on the homepage with a correctly-flagged `zonaryos_session` cookie (`httpOnly`, `secure`), and independently via a manual `curl` walk of the same code -> token -> redirect chain. It does **not** prove Keycloak 26 itself honors `KC_HOSTNAME`/`KC_HTTP_RELATIVE_PATH`/`KC_PROXY_HEADERS` exactly as documented here - that part is real, run against the real image, for the first time at step 5 (against `localhost` directly, before nginx is even involved) and step 9 (against the public URL) of the command sequence above. If step 5's discovery-document issuer doesn't read `https://zonaryos.duckdns.org/auth/realms/zonaryos` exactly, stop there rather than continuing to steps 6-9 and report back what it actually said.

One incidental finding worth passing on: the self-signed certificate used for this local proof needed to be ECDSA, not Ed25519 - Chromium (and browsers generally) don't accept Ed25519 server certificates for TLS today. Irrelevant to the real deployment (Let's Encrypt via `certbot` issues RSA/ECDSA certs, never Ed25519), but worth knowing if anyone re-runs a similar local proof.

What else could not be verified locally: the real instance's Docker/OS environment, the real `certbot`/Let's Encrypt issuance, the existing nginx site files' actual style (this session has no SSH access to read them - see step 1), and the other five sites' actual behavior after a reload.

## How the security-headers/rate-limiting/backups batch was verified

This batch (security headers, `/auth/` rate limiting, Keycloak brute-force detection, automated Postgres backups) was verified in a different sandbox than the one described above, with its own tool availability - noted explicitly rather than assuming the same constraints applied.

**nginx config syntax**: this sandbox has no working `apt` access to install nginx (`archive.ubuntu.com`/`security.ubuntu.com` over plain HTTP are unreachable from here) and Docker Hub's CloudFront blob storage returned `403 Forbidden` on every pull attempt (`docker pull nginx:alpine` failed the same way `docker pull postgres:16` and `docker pull quay.io/keycloak/keycloak:26.0` did - this sandbox's registry-pull restriction is broader than the `quay.io`-only block the previous verification round disclosed). **`nginx -t` was not run for real.** `deploy/nginx/zonaryos.conf` was instead checked by careful reading against nginx's documented `add_header`/`limit_req_zone` inheritance and context rules (cited in the file's own comments) - genuinely lower confidence than an actual syntax check, said plainly rather than implied otherwise. `docs/DEPLOYMENT.md` step 6 above still runs the real `sudo nginx -t` on the actual box, which is where this gets its first real check.

**Content-Security-Policy**: verified for real, not just read. `web/` was `npm ci`'d and `npm run build`'d (a real production build, Next.js 16/Turbopack, no errors), then `npm run start`'d on port 3005. Since nginx itself couldn't be installed, a small Node HTTP proxy (not part of this handoff, scratch-only) stood in for it, adding the exact same four header lines `deploy/nginx/zonaryos.conf` now sends (`Strict-Transport-Security`, `X-Content-Type-Options`, `X-Frame-Options`, and the literal `Content-Security-Policy` string from the config file) in front of the real built app. A real headless Chromium session (Playwright, already available in this sandbox) loaded the proxied page, and the result was: `200`, page content rendered (`h1` read "ZonaryOS"), the app hydrated (the sign-in link was interactive), and zero browser console errors of any kind - specifically zero CSP-violation ("Refused to ...", "Content Security Policy") messages. The two CSP-relevant facts this depended on were confirmed by inspecting `web/`'s actual source and the actual built HTML, not assumed: Next.js's App Router injects an inline, non-nonced `<script>` for RSC hydration (hence `script-src 'self' 'unsafe-inline'`) and its built-in not-found/error boundary renders an inline `<style>` (hence `style-src 'self' 'unsafe-inline'`); `next/font/google` (Geist/Geist Mono) self-hosts font files under this app's own `/_next/static/media/` at build time, and a full `grep` of `web/src` found no `<img>`/`next/image` usage and no external `http(s)://` script/stylesheet/CDN reference anywhere - so `default-src 'self'` genuinely covers the rest.

**Backup script**: `deploy/backup/backup-postgres.sh` was run completely unmodified, exercising its real logic, not a rewritten test version. Docker image pulls being blocked meant the real containerized Postgres from `docker-compose.yml` couldn't be brought up, so a real local Postgres 16 (already installed on this sandbox's host, independent of Docker) stood in for it, reached through a throwaway `docker` shim placed earlier on `PATH` that forwards exactly the one invocation the script makes (`docker compose -f ... -f ... exec -T postgres pg_dump -U <user> -d <db>`) to that local server - the script itself never knows the difference. Confirmed: a real `pg_dump` ran against a real Postgres with seeded test data (`CREATE TABLE demo; INSERT ...`), the gzip output passed `gzip -t` and, decompressed, contained the actual `CREATE TABLE public.demo` / `COPY public.demo (id) FROM stdin` statements and seeded rows - not a stub. The dump landed in a plain directory outside anything Docker manages, never inside a Docker volume. Rotation was tested by seeding 8 extra fake dump files with distinct old mtimes (10 total after a real run) and re-running the unmodified script: it correctly deleted exactly the 3 oldest and kept the 7 newest (2 real dumps plus the 5 next-oldest fakes). The two failure-safety paths (missing repo directory, and the `/var/lib/docker*` guard pattern) were also exercised and exit non-zero without writing a partial file. **Not verified**: the real cron entry actually firing unattended on the real box (cron doesn't run in this session), and the real named-container resolution via the real `docker-compose.yml`/`docker-compose.prod.yml` `postgres` service under real Docker networking (`network_mode: host`, port 5433) - the shim bypasses `docker compose`'s actual container resolution and host-networking specifics entirely, testing the script's own logic (dump, gzip, safety checks, rotation, file placement) rather than the Docker plumbing around it.

**Keycloak realm JSON**: `deploy/keycloak/zonaryos-realm.json` was checked as well-formed JSON with `python3 -m json.tool` (passed) after adding `bruteForceProtected: true` and Keycloak's standard companion thresholds (`maxFailureWaitSeconds`, `waitIncrementSeconds`, `quickLoginCheckMilliSeconds`, `maxDeltaTimeSeconds`, `failureFactor: 5`, `permanentLockout: false`, `minimumQuickLoginWaitSeconds`) at the realm's top level, matching Keycloak's `RealmRepresentation` field names. **Not verified**: that Keycloak 26 actually starts with this realm file and imports these fields without error, or that brute-force lockout actually engages after `failureFactor` failed logins - both would need a real Keycloak container, and this sandbox's Docker Hub/`quay.io` pulls are blocked (confirmed by direct attempt, not assumed - see above). `make dev-up-standalone` (which needs the same Keycloak image) could not be run for the same reason. This is a known, previously-disclosed limitation of this project's local verification, not a new one - the original deployment package's own "How this was verified" section above already flagged `quay.io` as blocked; this session additionally confirmed the block extends to Docker Hub's image CDN too.

**Go**: no Go files were touched this batch. `go build ./... && go vet ./... && go test ./...` were run anyway as a sanity check that nothing else broke.

**Explicitly skipped, and why**: the full `web` lint/i18n/audit suite (`npm run lint`, `check:i18n`, `check:audit`) - no UI strings or application code changed this batch, only `npm run build` was needed to produce a real artifact to test the CSP against. `scripts/check_doc_sync.py`, `check_api_contract.py`, `check_migration_safety.py` - PR-diff checks against `origin/main`, not meaningful to run standalone without that comparison in this environment. `gitleaks` - not installed in this sandbox and no secrets were introduced (the backup script and configs contain no credentials; the realm file's seeded dev password already existed unchanged). The full E2E smoke test and the original deployment package's own headless-browser Keycloak stand-in - not re-run, since nothing about the OIDC/login flow itself changed this batch, only headers/rate-limiting/backups layered on top of an already-verified topology.
