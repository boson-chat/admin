// Command fakestack serves internal/fakestack for the Updates browser
// checks (scripts/e2e-updates.sh): a Boson host with auth four releases
// behind, the web client one behind, and an api release that is breaking,
// runs a migration and never gets healthy (to walk through a rollback).
//
//	go run ./dev/fakestack -addr 127.0.0.1:18700
//	POST /__reset puts the scenario back.
package main

import (
	"flag"
	"log"
	"net/http"
	"sync/atomic"

	"github.com/boson-chat/admin/internal/fakestack"
)

const harbor = "harbor.floret.dev/boson/"

func scenario(base string) *fakestack.Stack {
	fs := fakestack.New(map[string]string{
		"auth": harbor + "auth:1.20.0", "api": harbor + "backend:0.57.0", "web": harbor + "web:0.100.0",
		"quicvoiced": harbor + "quicvoice:1.15.0", "ergo": harbor + "stack-ergo:2.19.1", "admin": harbor + "admin:1.26.0",
		"postgres": "postgres:18-alpine",
	})
	fs.Env = map[string]string{"AUTH_IMAGE": harbor + "auth:1.20.0", "API_IMAGE": harbor + "backend:0.57.0",
		"WEB_IMAGE": harbor + "web:0.100.0", "QUICVOICE_IMAGE": harbor + "quicvoice:1.15.0", "ADMIN_IMAGE": harbor + "admin:1.26.0"}
	rel := func(tag, at, body string) fakestack.Release {
		return fakestack.Release{Tag: tag, Name: tag, Body: body, At: at, URL: "https://github.com/boson-chat/x/releases/tag/" + tag}
	}
	fs.Releases = map[string][]fakestack.Release{
		"boson-chat/auth": {
			rel("v1.22.0", "2026-11-24T12:20:34Z", "## [1.22.0](https://github.com/boson-chat/auth/compare/v1.21.1...v1.22.0) (2026-11-24)\n\n### Features\n\n* send users back to the app after email verification ([#26](https://github.com/boson-chat/auth/issues/26))"),
			rel("v1.21.1", "2026-11-22T03:46:04Z", "## [1.21.1](https://github.com/boson-chat/auth/compare/v1.21.0...v1.21.1) (2026-11-22)\n\n### Bug Fixes\n\n* **roles:** a refresh race when a role is removed"),
			rel("v1.21.0", "2026-11-22T03:30:51Z", "## [1.21.0](https://github.com/boson-chat/auth/compare/v1.20.1...v1.21.0) (2026-11-22)\n\n### Features\n\n* role channels: auto-join on login\n* `WEB_CLIENT_URL` for email links"),
			rel("v1.20.1", "2026-11-20T10:00:00Z", "## [1.20.1](https://github.com/boson-chat/auth/compare/v1.20.0...v1.20.1) (2026-11-20)\n\n### Bug Fixes\n\n* <script>alert('not rendered')</script> typo in the reset email"),
			rel("v1.20.0", "2026-11-18T10:00:00Z", ""),
		},
		"boson-chat/backend": {
			rel("v0.58.0", "2026-11-25T09:00:00Z", "## [0.58.0](https://github.com/boson-chat/backend/compare/v0.57.0...v0.58.0) (2026-11-25)\n\n### ⚠ BREAKING CHANGES\n\n* nick claims need `NICK_CLAIM_DOMAIN` set in the stack's .env before upgrading\n\n### Features\n\n* nick claims on more networks"),
			rel("v0.57.0", "2026-11-15T09:00:00Z", ""),
		},
		"boson-chat/boson":     {rel("v0.101.0", "2026-11-26T15:21:11Z", "## [0.101.0](https://github.com/boson-chat/boson/compare/v0.100.0...v0.101.0)\n\n### Features\n\n* **video:** tiles follow the speaker"), rel("v0.100.0", "2026-11-20T10:00:00Z", "")},
		"boson-chat/quicvoice": {rel("v1.15.0", "2026-11-21T15:27:51Z", "")},
		"ergochat/ergo":        {rel("v2.19.1", "2026-10-01T00:00:00Z", "")},
		"boson-chat/admin":     {rel("v1.26.0", "2026-11-22T03:31:22Z", "")},
	}
	for _, tag := range []string{"auth:1.20.1", "auth:1.21.0", "auth:1.21.1", "auth:1.22.0", "backend:0.58.0", "web:0.101.0"} {
		fs.Tags["boson/"+tag] = true
	}
	fs.Broken[harbor+"backend:0.58.0"] = true
	fs.Migrates[harbor+"backend:0.58.0"] = "api"
	fs.Handler(base)
	return fs
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18700", "listen address")
	flag.Parse()
	base := "http://" + *addr
	var cur atomic.Pointer[fakestack.Stack]
	cur.Store(scenario(base))
	log.Printf("fake stack on %s", base)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__reset" && r.Method == http.MethodPost {
			cur.Store(scenario(base))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		cur.Load().ServeHTTP(w, r)
	})))
}
