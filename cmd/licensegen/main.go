// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Command licensegen is a reference license-token issuing tool for
// LOCAL/TEST/DEV USE ONLY.
//
// IMPORTANT - what this is NOT: this is a stand-in for the real,
// still-undesigned central-server license issuance flow that Open Points
// item 34 ("Deployment Target / Infrastructure") and item 32 ("License
// Verification Mechanism") both leave open. There is no ZonaryOS-run
// issuance service, no customer/billing record, no revocation mechanism,
// and no real module/pricing catalog (item 30 is fully open) behind this
// tool - it is a thin CLI wrapper around internal/license.Sign, useful
// for generating a token to exercise ZONARYOS_LICENSE_ENFORCEMENT=true
// locally (see docs/DEVELOPMENT.md's License Verification section) or in
// this batch's own tests. Whoever eventually builds the real central
// server (item 34) will need a genuinely different, server-side issuance
// system with real authentication, auditing, and revocation - not more
// of this CLI.
//
// # Generating a test keypair
//
//	go run ./cmd/licensegen -genkey
//
// prints a fresh Ed25519 public/private key pair (base64url-encoded) and
// exits without issuing a token. Keep the private key out of the repo and
// out of any committed file - export it as an env var or a
// gitignored local file only. (Equivalent one-liner without this tool,
// for reference: `openssl genpkey -algorithm ed25519` produces a PEM key
// that would need re-encoding to this package's raw base64url form, so
// `-genkey` above is the simpler path for this codebase's format.)
//
// # Issuing a token
//
//	export ZONARYOS_LICENSE_PRIVATE_KEY=<private key from -genkey>
//	go run ./cmd/licensegen \
//	  -issued-to "local-dev-firm" \
//	  -expires-in 720h \
//	  -modules platform_admin,audit_trail
//
// prints a signed token string on stdout - set it as
// ZONARYOS_LICENSE_TOKEN, and set ZONARYOS_LICENSE_PUBLIC_KEY to the
// matching public key from -genkey, to exercise
// ZONARYOS_LICENSE_ENFORCEMENT=true locally.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/license"
)

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
}

func main() {
	genKey := flag.Bool("genkey", false, "generate a new Ed25519 keypair for local/test use and exit (does not issue a token)")
	privateKeyPath := flag.String("private-key", "", "path to a file containing the base64url-encoded Ed25519 private key used to sign the token (falls back to ZONARYOS_LICENSE_PRIVATE_KEY if unset)")
	issuedTo := flag.String("issued-to", "", "opaque firm/installation identifier this token is issued to (required unless -genkey)")
	expiresIn := flag.Duration("expires-in", 30*24*time.Hour, "how long from now the token remains valid")
	modules := flag.String("modules", "", "comma-separated module keys to activate (see internal/license's Module* constants - platform_admin, audit_trail, workflow_engine)")
	flag.Parse()

	if *genKey {
		runGenKey()
		return
	}

	if *issuedTo == "" {
		slog.Error("licensegen: -issued-to is required (unless -genkey is set)")
		os.Exit(1)
	}

	privRaw := os.Getenv("ZONARYOS_LICENSE_PRIVATE_KEY")
	if *privateKeyPath != "" {
		b, err := os.ReadFile(*privateKeyPath)
		if err != nil {
			slog.Error("licensegen: read -private-key file", "err", err)
			os.Exit(1)
		}
		privRaw = strings.TrimSpace(string(b))
	}
	if privRaw == "" {
		slog.Error("licensegen: a private key is required - pass -private-key <file> or set ZONARYOS_LICENSE_PRIVATE_KEY (generate one with `go run ./cmd/licensegen -genkey`). This tool NEVER reads a private key from this repository - there is none committed here.")
		os.Exit(1)
	}

	priv, err := license.ParsePrivateKey(privRaw)
	if err != nil {
		slog.Error("licensegen", "err", err)
		os.Exit(1)
	}

	var moduleList []string
	if strings.TrimSpace(*modules) != "" {
		for _, m := range strings.Split(*modules, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				moduleList = append(moduleList, m)
			}
		}
	}

	tok := license.Token{
		IssuedTo:  *issuedTo,
		ExpiresAt: time.Now().Add(*expiresIn),
		Modules:   moduleList,
	}

	tokenStr, err := license.Sign(priv, tok)
	if err != nil {
		slog.Error("licensegen: sign token", "err", err)
		os.Exit(1)
	}

	fmt.Println(tokenStr)
}

func runGenKey() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		slog.Error("licensegen: generate keypair", "err", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "Test/dev Ed25519 keypair - NEVER commit the private key to this repository.")
	fmt.Printf("ZONARYOS_LICENSE_PUBLIC_KEY=%s\n", base64.RawURLEncoding.EncodeToString(pub))
	fmt.Printf("ZONARYOS_LICENSE_PRIVATE_KEY=%s\n", base64.RawURLEncoding.EncodeToString(priv))
}
