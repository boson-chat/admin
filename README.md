# boson admin

The web admin panel for [boson](https://github.com/boson-chat/stack): manage
accounts, sessions and invites through the
[auth service](https://github.com/boson-chat/auth), read the audit log, and
see the health of the whole stack (service checks, versions, TLS certificate
expiry, live voice and sign-in numbers).

A SvelteKit single-page app is embedded in a small Go backend-for-frontend
(BFF). The browser only ever talks to the BFF; the BFF holds every upstream
URL and secret and calls the auth service with the signed-in admin's tokens.

```text
browser ──HTTPS──► Traefik ──► admin :8080  ── /api ──► auth :8080   (login, refresh, admin API)
   │                             │                ├────► auth :8081   (health, version)
   │                             │                ├────► quicvoiced :8080 (health, version)
   │                             │                ├────► VictoriaMetrics (PromQL, optional)
   │                             │                ├────► ergo :6697   (IRC: CAP LS + registration, optional)
   │                             │                └────► host:port TLS handshakes (cert expiry)
   │                             └── :8081 /metrics, /healthz (internal only)
   └──► PUBLIC_PROBE_TARGETS (latency "from you": https:// GETs, wss:// opens; optional)
```

## What it shows

- **Stack:** the edge (`EDGE_NAME`, Traefik by default) graded by its
  certificates, each service from `HEALTH_TARGETS` (up / degraded / down, latency,
  the failing check's status and body), IRC from `IRC_CHECK` (up / down,
  handshake latency and the Ergo version), versions from `VERSION_TARGETS`, the
  certificates in `TLS_CHECK_HOSTS` (valid, renews soon, expiring, expired,
  untrusted or unreachable; days left; issuer), and, when `METRICS_URL` is set,
  live numbers with one-hour trends: QUIC connections, live rooms,
  participants, audio frames forwarded per second, logins in the last hour by
  outcome, refresh-token reuse (24 h) and rate-limited requests (1 h). Without
  `METRICS_URL` it says metrics are off. A Grafana link appears when
  `GRAFANA_URL` is set. Refreshes every 15 s while the tab is visible.
  Each row shows two latencies: **internal** (the panel's own check over the
  container network) and **from you** (timed by your browser against the
  service's public endpoint from `PUBLIC_PROBE_TARGETS`; see below).
- **Users:** search by username or email, paginate, open a user to change their
  role, disable or enable them (disabling signs them out everywhere), list and
  revoke their sessions, and see their recent activity.
- **Invites:** create (uses, expiry, note), with the code shown once; list with
  status (active, used up, expired, revoked); revoke. Warns when registration
  is open or closed rather than invite-only.
- **Audit log:** filter by event (or group: admin actions, sign-ins,
  password events), actor, and time range; usernames are resolved for the actor
  and for the account an admin action affected.
- **Sign-in providers:** a placeholder for OIDC (auth phase 2) and SAML
  (phase 3). It lists providers the auth service already advertises in
  `/v1/config`.

IRC isn't managed from the panel, but it is monitored: with `IRC_CHECK` set
the panel connects to the IRC server (Ergo in the stack) every overview poll
(cached for 30 s), sends `CAP LS 302`, and reports it up once the server
answers. It then registers with a throwaway nick (`bosonadm…`), reads the
version from `RPL_MYINFO` (004) and quits cleanly; if registration doesn't
finish in time the server still counts as up, just without a version. The TLS
certificate is not judged here (that's `TLS_CHECK_HOSTS`' job), so an expiring
cert doesn't read as "IRC down". Without `IRC_CHECK` the IRC row says
"not configured".

### Latency from your browser

The overview hands the browser `PUBLIC_PROBE_TARGETS` plus the panel's own
`/healthz` (shown on the edge, "from you to this panel"). The browser times
them with `performance.now()`:

- `https://` / `http://`: `fetch(url, {mode: 'no-cors', cache: 'no-store'})`,
  five requests, the first discarded as the DNS/TCP/TLS warm-up, median of the
  other four. The response is opaque (health endpoints send no CORS headers),
  so a 404 still times the round trip; only a network failure shows "n/a".
  Re-run on every poll (≥ 14 s apart) and on "Check now".
- `wss://` / `ws://`: time until a WebSocket opens, then it is closed. One
  open per run, at most every 2 minutes (30 s with "Check now"), because each
  open is an IRC connection and IRC servers throttle connections per IP (Ergo:
  32 per 10 minutes, then a temporary ban). The origin must be allowed by the
  IRC server's WebSocket origin list (`IRC_WEBSOCKET_ORIGINS` in the stack).

Every origin in `PUBLIC_PROBE_TARGETS` (and nothing else) is added to the
CSP's `connect-src`.

## Running locally

Needs Docker, Go 1.27, Node 24+, and the `auth` and `quicvoice` repos checked
out next to this one (`../auth`, `../quicvoice`).

```sh
make dev-env      # writes .env with random dev secrets
make dev-up       # postgres, mailpit, auth, admin, caddy, ergo, quicvoiced, victoriametrics
docker compose exec auth ./auth user create --username admin --email admin@example.com
docker compose exec auth ./auth user set-role admin admin
```

Open http://localhost:8090 (or https://admin.localhost:8443 through Caddy with
its local CA). The dev auth service runs with `REGISTRATION=invite`; Mailpit
is on http://localhost:8025 and VictoriaMetrics on http://localhost:8428.

The end-to-end check signs in through the panel and exercises users,
disable/enable, invites, the audit log and the overview:

```sh
ADMIN_USER=admin ADMIN_PASSWORD_FILE=./pw ./scripts/e2e.sh   # needs curl + jq
```

For UI work, run the BFF from the compose (`:8090`) and `make ui-dev`: Vite
on http://localhost:5173 proxies `/api` to it.

```sh
make test        # go test -race ./... (uses a UI placeholder if ui/build is empty)
make lint        # go vet + gofmt
make ui-check    # svelte-check, prettier, eslint
cd ui && npm test                                         # vitest: Dropdown + helpers
cd ui && ADMIN_PASSWORD_FILE=../pw npm run test:e2e       # Playwright vs the dev compose (desktop + mobile)
make build       # UI + Go binary in bin/admin
```

## Configuration

Everything is an environment variable. Durations use Go syntax (`15m`, `12h`).

| Variable | Default | |
|---|---|---|
| `ENV` | `development` (image: `production`) | `development` = console logs and allows an empty `SESSION_KEY` |
| `VERSION` | `dev` (set by the image) | shown in the UI, logs and metrics |
| `LOG_LEVEL` | `info` | zerolog level |
| `HTTP_ADDR` | `:8080` | UI and `/api`; put it behind the reverse proxy |
| `INTERNAL_ADDR` | `:8081` | `/metrics`, `/healthz`, `/version`; never publish it. Empty disables it |
| `PUBLIC_URL` | `http://localhost:8080` | the panel's external URL, e.g. `https://admin.example.com`. State-changing requests must come from this origin |
| `TRUSTED_PROXIES` | – | comma-separated CIDRs/IPs whose `X-Forwarded-For` is honoured (the proxy's container network) |
| `SESSION_KEY` | **required** outside development | 32 random bytes, base64 or hex (`openssl rand -base64 32`). Encrypts and authenticates the session cookie. Changing it signs everyone out |
| `COOKIE_SECURE` | `true` | `false` only for plain-http dev. When true the cookie is `__Host-boson_admin` |
| `SESSION_MAX_AGE` | `12h` | absolute lifetime of a panel sign-in (also capped by the refresh token's lifetime) |
| `AUTH_URL` | `http://auth:8080` | auth service public API |
| `AUTH_INTERNAL_URL` | – | auth internal listener, e.g. `http://auth:8081`; defaults the auth health checks and version |
| `METRICS_URL` | – | Prometheus-compatible query API, e.g. `http://victoriametrics:8428`. Empty = "metrics not configured" |
| `GRAFANA_URL` | – | Grafana base URL for links |
| `HEALTH_TARGETS` | auth `/healthz` + `/readyz` from `AUTH_INTERNAL_URL` | `name=url,…`. Repeat a name to group checks under one service. 2xx/3xx = healthy; redirects aren't followed |
| `VERSION_TARGETS` | auth from `AUTH_INTERNAL_URL/metrics` | `name=url,…`: plain text (quicvoiced `/version`), JSON `{"version"}`, or a Prometheus `/metrics` URL (reads `target_info{service_version}`) |
| `TLS_CHECK_HOSTS` | – | `host:port[@dial-host:dial-port],…`. The name is sent as SNI and verified; the optional dial address lets you check a public name over the internal network. Results are cached 5 minutes |
| `IRC_CHECK` | – | IRC server to probe: `ircs://host[:6697]` (TLS) or `irc://host[:6667]`, e.g. `ircs://ergo:6697`. Empty = IRC "not configured" |
| `IRC_CHECK_SERVER_NAME` | `IRC_CHECK`'s host | TLS SNI for the probe, e.g. the public `IRC_HOST` when dialling the container |
| `PUBLIC_PROBE_TARGETS` | – | `name=url,…` public endpoints your browser times (`https://`, `http://`, `wss://`, `ws://`). Names match services (`auth`, `api`, `quicvoiced`, `irc`); `admin` overrides the panel's own `/healthz`. Origins are added to the CSP `connect-src` |
| `EDGE_NAME` | `Traefik` | what the stack view calls the reverse proxy in front of everything |
| `UPSTREAM_TIMEOUT` | `5s` | per upstream request (health and TLS checks use at most 3 s / 5 s) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | – | enable OTLP/HTTP metrics push in addition to `/metrics` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` / `OTEL_EXPORTER_OTLP_METRICS_PROTOCOL` | `http/protobuf` | only `http/protobuf` is supported |
| `OTEL_METRICS_EXPORTER` | – | `none` turns OTLP push off |

The live numbers assume the metrics backend scrapes the services' Prometheus
endpoints (`auth:8081/metrics`, `quicvoiced:8080/metrics`), or ingests OTLP
with Prometheus naming, so the series are named as in the
[quicvoice](https://github.com/boson-chat/quicvoice#metrics) and
[auth](https://github.com/boson-chat/auth#metrics) READMEs.

## Adding it to the stack

One service plus a Traefik router on `admin.<domain>`. It needs only the
auth service on the internal network (and optionally quicvoiced and
VictoriaMetrics). Adjust the entrypoint, cert resolver and network names to
the stack's Traefik setup:

```yaml
  admin:
    <<: *restart
    profiles: [admin]
    image: ${ADMIN_IMAGE:-harbor.floret.dev/boson/admin:latest}
    environment:
      PUBLIC_URL: https://${ADMIN_HOST:?set ADMIN_HOST in .env}
      SESSION_KEY: ${ADMIN_SESSION_KEY:?run scripts/setup.sh}   # openssl rand -base64 32
      TRUSTED_PROXIES: ${PROXY_SUBNET:-172.16.0.0/12}
      AUTH_URL: http://auth:8080
      AUTH_INTERNAL_URL: http://auth:8081
      HEALTH_TARGETS: >-
        auth=http://auth:8081/healthz,auth=http://auth:8081/readyz,
        quicvoiced=http://quicvoiced:8080/healthz
      VERSION_TARGETS: auth=http://auth:8081/metrics,quicvoiced=http://quicvoiced:8080/version
      TLS_CHECK_HOSTS: ${IRC_HOST}:6697,${VOICE_HOST}:443,${ADMIN_HOST}:443
      IRC_CHECK: ircs://ergo:6697                 # irc profile; leave unset without it
      IRC_CHECK_SERVER_NAME: ${IRC_HOST}
      PUBLIC_PROBE_TARGETS: >-
        auth=https://${AUTH_HOST}/healthz,api=https://${API_HOST}/health,
        quicvoiced=https://${VOICE_HOST}/healthz,irc=wss://${IRC_HOST}/webirc
      METRICS_URL: ${METRICS_URL:-}            # http://victoriametrics:8428 when enabled
      GRAFANA_URL: ${GRAFANA_URL:-}
    labels:
      traefik.enable: "true"
      traefik.http.routers.admin.rule: Host(`${ADMIN_HOST}`)
      traefik.http.routers.admin.entrypoints: websecure
      traefik.http.routers.admin.tls.certresolver: ${TRAEFIK_CERTRESOLVER:-letsencrypt}
      traefik.http.services.admin.loadbalancer.server.port: "8080"
    depends_on: [auth]
```

- `ADMIN_HOST=admin.<domain>` needs a DNS record like the other hostnames.
- Don't route port 8081; VictoriaMetrics can scrape `admin:8081/metrics`.
- So that sessions and the audit log record the admin's real IP, add the panel's
  network to the auth service's `TRUSTED_PROXIES` (the panel forwards
  `X-Forwarded-For`). Otherwise every panel sign-in shows the panel's IP and
  shares one per-IP rate-limit bucket.
- Run one replica (see "Sessions" below), or use sticky sessions.

## How it works

### Sessions

- Sign-in posts to the auth service's `/v1/login` in bearer mode. Only
  `role=admin` gets a panel session; anyone else's new upstream session is
  logged out straight away.
- The access and refresh tokens are sealed into one HttpOnly, `SameSite=Lax`,
  `Secure` cookie with AES-256-GCM under `SESSION_KEY`. The cookie name is
  bound as additional data. Page scripts never see a token; the BFF keeps no
  session store.
- Access tokens are refreshed 30 s before they expire, and once more if the
  auth service answers 401. The new tokens are written back to the cookie on
  the same response.
- **Refresh single-flight:** the auth service rotates refresh tokens and
  revokes the session when one is reused. Concurrent requests carrying the same
  cookie share one refresh call, and the result is remembered for two minutes
  so a request that still carries the old cookie gets the new tokens instead
  of tripping reuse detection. The memory is per process, which is why one
  replica (or sticky sessions) is recommended.
- `GET /api/session` and the overview re-check the admin against `/v1/me`, so
  a disabled or demoted admin is signed out on the next page load or poll.
  Admin API calls are authorised by the auth service on every request.
- Signing out revokes the upstream session.

### CSRF and browser hardening

- State-changing `/api` calls need `X-CSRF-Token` matching the token sealed in
  the session (handed to the UI by `/api/session` and on login). Sign-in, which
  has no session yet, needs an `X-Requested-With` header.
- Every state-changing request is also refused when `Origin` isn't
  `PUBLIC_URL`'s origin or `Sec-Fetch-Site` is cross-site. JSON-only bodies
  are a further barrier.
- A strict CSP is sent: scripts only from the panel itself plus the SHA-256 of
  SvelteKit's inline bootstrap, computed from the embedded `index.html` at
  startup; `connect-src` is `'self'` plus exactly the origins of
  `PUBLIC_PROBE_TARGETS`; `frame-ancestors 'none'`, `base-uri 'none'`,
  `object-src 'none'`. There are no other third-party requests (fonts are
  bundled).

### API

All under `/api`, JSON. Errors are `{"error": "<code>", "message": "<text>"}`;
the auth service's 4xx codes and messages pass through, 5xx details don't.

| Method & path | |
|---|---|
| `GET /api/session` | `{user, csrf_token, expires_at, features: {version, grafana_url, metrics}}` or 401 |
| `POST /api/login` | `{login, password}` → session (sets the cookie) |
| `POST /api/logout` | 204, clears the cookie |
| `GET /api/users?q=&limit=&offset=` | `{users, total, limit, offset}` |
| `GET /api/users/{id}`, `PATCH /api/users/{id}` `{role}` | |
| `POST /api/users/{id}/disable`, `/enable` | |
| `GET /api/users/{id}/sessions` | marks the panel's own session `current` |
| `GET /api/users/{id}/audit?limit=` | entries the user performed |
| `DELETE /api/sessions/{id}` | revoke (not the panel's own session) |
| `GET /api/invites`, `POST /api/invites` `{max_uses, ttl, note}`, `DELETE /api/invites/{id}` | the code is only in the create response |
| `GET /api/audit?event=&user_id=&since=&limit=` | `event` exact or prefix with `*`; `since` RFC 3339 or a duration (`24h`) |
| `GET /api/overview` | services (incl. `irc`), certificates, metrics, `edge`, `irc`, `public_probes` |
| `GET /api/sso` | placeholder: providers from `/v1/config` and the roadmap |

### Metrics

On `INTERNAL_ADDR` `/metrics` (and OTLP if configured), plus Go runtime metrics:

| Metric | Labels |
|---|---|
| `admin_http_requests_total` | `http_route` (mux pattern), `status_class` |
| `admin_http_duration_seconds` | `http_route` |
| `admin_upstream_requests_total` | `upstream`, `op`, `outcome` (status class or `error`) |
| `admin_upstream_duration_seconds` | `upstream` |
| `admin_logins_total` | `outcome` (`success`, `invalid_credentials`, `not_admin`, `rate_limited`, `error`) |
| `admin_token_refreshes_total` | `outcome` (`success`, `shared`, `expired`, `error`) |

## Layout

```text
cmd/main.go                    → internal/commands (cobra: serve, version)
config/                        env config (configor)
http/server.go                 dependency wiring + listeners
http/handlers                  /api handlers, session/CSRF wrapper, SPA server
http/middleware                client IP, logging/metrics, security headers
internal/services/admin        sign-in, token refresh (single-flight), admin operations
internal/services/overview     health, versions, certificates, PromQL stats
internal/clients/{authapi,health,tlsprobe,ircprobe,promql}   upstream clients behind interfaces
internal/session               sealed session cookie
metrics/                       OpenTelemetry instruments + Prometheus exporter
ui/                            SvelteKit app (adapter-static), embedded via ui/embed.go
scripts/e2e.sh                 end-to-end check against the dev compose
ui/src/lib/components/Dropdown.svelte   the panel's select (WAI-ARIA select-only combobox)
ui/e2e/                        Playwright checks (dropdown keyboard/mouse, browser latency)
```

## Release

Conventional commits on `main` → semantic-release tags `vX.Y.Z` and pushes
`harbor.floret.dev/boson/admin:X.Y.Z` and `:latest` (linux/amd64 + arm64).
The workflow needs the repo secrets `HARBOR_USERNAME` / `HARBOR_PASSWORD`;
the Harbor `boson` project is public, so pulls need no credentials.
