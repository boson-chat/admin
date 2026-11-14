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
- **Services:** every container of the compose project with its state, uptime,
  image version, restarts, CPU and memory; restart, stop and start (with a
  confirmation, and the service name typed for the panel itself and the
  edge); a log viewer per service (tail 100–5,000 lines, follow, filter,
  stderr only, download). Through a locked-down Docker socket proxy; see
  "Services control" below.
- **Network settings:** registration (open, invite only, closed), required
  email verification, and who may create text channels and voice rooms.
- **Directory:** whether the network is listed in the boson.chat directory,
  and (once api.boson.chat supports it) submitting it with a DNS TXT proof.
  See "Directory" below.
- **Users:** search by username or email, paginate, create a user (with a
  password or an emailed set-password link), open a user to change their
  role, disable or enable them (disabling signs them out everywhere), send a
  password reset or a new verification email, see their linked SSO sign-ins,
  their boson profile (avatar, display name, handle) and the servers they
  list in the directory, list and revoke their sessions, and see their recent
  activity.
- **Invites:** create (uses, expiry, note), with the code shown once; list with
  status (active, used up, expired, revoked); revoke. Warns when registration
  is open or closed rather than invite-only.
- **Audit log:** filter by event (or group: admin actions, sign-ins,
  SSO, password events), who did it, the affected account, and a time window;
  scrolls through the whole history page by page. Usernames are resolved for
  the actor and for the account an admin action affected.
- **Sign-in providers:** add, edit, test, switch on/off and remove OpenID
  Connect and SAML providers, with the callback / ACS / SP metadata URLs to
  copy and setup steps for Google, Authentik, Zitadel, Keycloak, Okta and
  Microsoft Entra ID. Client secrets are write-only.

Features whose upstream endpoint doesn't exist yet (an older auth release,
a boson API without the admin profile read) show what they need ("needs
auth ≥ v1.4.0") instead of an error; see "Upstream work".

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

Services control in dev: put `DOCKER_PROXY_URL=http://docker-proxy:2375` in
`.env` and `docker compose --profile docker up -d` (the proxy runs as root
there for Docker Desktop's socket; on Linux set `DOCKER_PROXY_USER=65534:<docker gid>`).

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
| `DOCKER_PROXY_URL` | – | the Docker socket proxy, e.g. `http://docker-proxy:2375`. Empty = services control off. A `unix:` URL or a `docker.sock` path is refused |
| `DOCKER_PROJECT` | `boson` | compose project whose containers are shown and controlled; nothing outside it is touched |
| `DOCKER_PROTECTED_SERVICES` | `admin,traefik` | compose services whose name must be typed to stop or restart them (the panel's own container always is) |
| `DOCKER_TIMEOUT` | `20s` | per Docker call (stats take about a second) |
| `BOSON_API_URL` | – | the stack's boson API (`http://api:3000`), for users' boson profiles. Empty = profiles off |
| `BOSON_CDN_URL` | `https://cdn.boson.chat` | where avatars and server icons are served from; added to the CSP `img-src` |
| `DIRECTORY_API_URL` | `https://api.boson.chat` | the public directory. Empty = the directory page is off |
| `DIRECTORY_REGISTRATION` | `false` | use the self-hosted registration API (not live on api.boson.chat yet) |
| `DIRECTORY_KEY` | `SESSION_KEY` | secret the directory management key is derived from. Set it if you rotate `SESSION_KEY`, or the panel loses edit access to its listing |
| `DIRECTORY_HOSTNAME` | `IRC_CHECK_SERVER_NAME` | the IRC hostname to list |
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

With services control (see below) the panel also joins the proxy's network:

```yaml
  admin:
    environment:
      DOCKER_PROXY_URL: http://docker-proxy:2375
      DOCKER_PROJECT: ${COMPOSE_PROJECT_NAME:-boson}
      BOSON_API_URL: http://api:3000
    networks: [default, docker-proxy]
```

- `ADMIN_HOST=admin.<domain>` needs a DNS record like the other hostnames.
- Don't route port 8081; VictoriaMetrics can scrape `admin:8081/metrics`.
- So that sessions and the audit log record the admin's real IP, add the panel's
  network to the auth service's `TRUSTED_PROXIES` (the panel forwards
  `X-Forwarded-For`). Otherwise every panel sign-in shows the panel's IP and
  shares one per-IP rate-limit bucket.
- Run one replica (see "Sessions" below), or use sticky sessions.

## Services control

The panel never mounts the Docker socket. It talks HTTP to
[wollomatic/socket-proxy](https://github.com/wollomatic/socket-proxy), which
holds the socket and forwards only an allowlist of method + path regexes:

| Method | Allowed paths |
|---|---|
| `GET` | `/_ping`, `/version`, `/containers/json`, `/containers/{id}/json`, `/containers/{id}/logs`, `/containers/{id}/stats` |
| `HEAD` | `/_ping` |
| `POST` | `/containers/{id}/start`, `/containers/{id}/stop`, `/containers/{id}/restart` |

Everything else (exec, create, kill, delete, attach, archive, images,
volumes, events, …) gets 403 from the proxy. Only the host named `admin` may
connect, on an internal network nobody else joins. Add to the stack:

```yaml
  docker-proxy:
    <<: *restart
    profiles: [admin]
    image: wollomatic/socket-proxy:1.13.2
    # nobody + the host's docker group (getent group docker | cut -d: -f3)
    user: "65534:${DOCKER_GID:?set DOCKER_GID in .env}"
    read_only: true
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    mem_limit: 64m
    command:
      - -loglevel=info
      - -listenip=0.0.0.0
      - -allowfrom=admin
      - -watchdoginterval=600
      - -stoponwatchdog
      - -shutdowngracetime=5
      - '-allowGET=(/v1\.[0-9]+)?/(_ping|version|containers/json|containers/[a-zA-Z0-9_.-]+/(json|logs|stats))'
      - '-allowHEAD=(/v1\.[0-9]+)?/_ping'
      - '-allowPOST=(/v1\.[0-9]+)?/containers/[a-zA-Z0-9_.-]+/(start|stop|restart)'
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    networks: [docker-proxy]

networks:
  docker-proxy:
    internal: true
```

The proxy can't filter by label, so its scope is every container on the
host for those verbs. The panel narrows it: it lists with a
`com.docker.compose.project` filter and re-checks the label itself, resolves
every action by container name inside the project (anything else is "not
found"), skips one-off `run` containers, and re-checks the admin with the auth
service before every Docker call (a demoted admin's still-valid cookie can't
restart anything). Stopping or restarting a protected service
(`DOCKER_PROTECTED_SERVICES`, and the panel's own container, found by its
hostname) needs the service name typed, enforced by the BFF, not only the UI.
An action on the panel's own container runs half a second after the
response. Every action is logged (`actor`, `action`, `container`, `service`)
and counted in `admin_service_actions_total`.

Logs: the newest N lines (≤ 5,000 on screen, ≤ 50,000 in a download), then
polled every 2 s with Docker's `since` while "Follow" is on (no long-lived
streams through the edge).

Checked against the real proxy: `DOCKER_PROXY_TEST_URL=http://127.0.0.1:2375
DOCKER_PROXY_TEST_PROJECT=… DOCKER_PROXY_TEST_CONTAINER=… go test -run
TestAgainstProxy ./internal/clients/dockerapi/` (a container that writes to
stdout and stderr).

## Directory

The boson.chat directory (`GET https://api.boson.chat/servers`) lists IRC
networks that proved control of their hostname with a TXT record
`_boson.<hostname>` = `boson-verify=<token>` (checked against Cloudflare,
Google and Quad9; re-checked daily by the backend's `cron --mode=verify`).
Today a listing can only be created by a signed-in boson.chat user
(`POST /servers` with a boson.chat access token). A self-hosted network has
its own auth service, so its admins have no boson.chat token, and there is no
OAuth or device flow to get one.

The panel therefore uses a proposed registration API where the network
proves itself twice instead of through a user account: a **management key**
(HMAC-SHA256 of `DIRECTORY_KEY`/`SESSION_KEY` and the hostname, sent as
`Authorization: Network <key>`; the directory stores only its hash, so only
this panel can edit the listing and the panel stores nothing) and the
**DNS TXT record**. Until the backend has it, leave
`DIRECTORY_REGISTRATION=false`: the page still shows whether the hostname is
listed (from the public search) and explains the steps. Details in
"Upstream work".

## Upstream work

What the panel assumes and what is missing (as of auth v1.2.1 and the boson
backend on `main`). The panel detects a missing endpoint (Go's plain-text 404
or a 405) and shows "needs … ≥ version" instead of failing. The versions are
constants in `internal/clients/authapi/v2.go`.

**auth** (boson-chat/auth `main`: settings, admin users and audit land in
v1.3.0, SSO providers and identities in v1.4.0). Checked against a locally
running auth service and Dex, not only against fakes; rerun with
`AUTH_TEST_URL=http://127.0.0.1:8080 AUTH_TEST_ADMIN=… AUTH_TEST_PASSWORD=… go test -run TestAgainstAuth ./internal/clients/authapi/`.

| Endpoint | Release | Used for |
|---|---|---|
| `GET/PATCH /v1/admin/settings` → `{registration, require_email_verification, channel_creation, voice_channel_creation, updated_at, updated_by}` | 1.3.0 | Network settings. `updated_*` are null until the first PATCH |
| `POST /v1/admin/users` `{username, email, role, password \| send_invite_email}` → 201 `{user, invite_email_sent}` | 1.3.0 | Create user |
| `POST /v1/admin/users/{id}/password-reset`, `…/verification` → 202 | 1.3.0 | Emails. `400 no_email`, `409 already_verified`, `409 account_disabled` (and `409 username_taken` / `email_taken` / `slug_taken`) get panel wording |
| `GET /v1/admin/audit?event=&actor=&target=&since=&until=&cursor=&limit=` → `{entries[{…, actor_user_id, actor_username, target_user_id, target_username}], next_cursor}` | 1.3.0 | Audit paging. `event` is an exact name or a comma list; the panel expands its `admin_*`-style groups into names. `next_cursor` is always present (`null` at the end), which is how the panel tells it from 1.2 |
| `GET /v1/admin/users/{id}` → `{user, identities[{id, provider_id, provider_slug, provider_kind, provider_display_name, subject, email, created_at, last_login_at}], has_password}` | 1.4.0 | Linked sign-ins, "SSO only" |
| `GET/POST /v1/admin/sso/providers`, `GET/PATCH/DELETE …/{id}`, `POST …/{id}/test` → `{ok, details{…}}` | 1.4.0 | Sign-in providers. `config` is one flat object per kind (OIDC: `issuer, client_id, client_secret (write-only), scopes, username_claim, email_claim, display_name_claim`; SAML: `idp_metadata_url \| idp_metadata_xml, entity_id, attribute_map, name_id_format, sign_requests`); the panel's own API nests it as `{oidc}` / `{saml}`. PATCH merges key by key: the panel omits `client_secret` to keep it and sends cleared optional keys as `null`. Responses carry `has_client_secret`, `start_url`, `redirect_uri` (OIDC), `acs_url`, `sp_metadata_url`, `sp_entity_id`, `sp_certificate` (SAML). Deleting a provider deletes its linked identities |

**boson API: `GET /v1/admin/profiles/{auth_user_id}`** (new). The backend
already verifies auth access tokens; the panel calls it with the signed-in
admin's own token. The backend must:

1. Gate on the token's `role` claim being `admin` (403 otherwise). Nothing
   checks `role` today.
2. Map `auth_user_id` (`usr_…`) to `users.id` via `auth_identities`
   (`auth_user_id → user_id`) without creating a row.
3. Answer `{"profile": {user_id, auth_user_id, handle, display_name,
   avatar_url, is_discoverable, handle_changed_at, created_at}, "servers":
   [{id, hostname, port, tls, name, verification_status, health_status,
   user_count, icon_url, registered_at}]}` where servers are
   `servers WHERE registered_by = users.id` in any status. No
   `encrypted_user_secret*`, no nick or bouncer data.
4. Answer 404 `{"error":"profile_not_found"}` (JSON) when the person has no
   profile yet; the panel shows "no boson profile yet". A plain-text 404
   means "endpoint missing".

**boson API: self-hosted directory registration** (new; behind
`DIRECTORY_REGISTRATION`). All calls carry `Authorization: Network <key>`
(43-char base64url); the backend stores `sha256(key)` in a new
`servers.management_key_hash` column and leaves `registered_by` NULL, so the
existing verify cron covers these rows unchanged.

| Endpoint | |
|---|---|
| `POST /directory/networks` `{hostname, port, tls, name, description?, tags?, languages?, is_nsfw?, website_url?}` | 201 `{network, verification: {record_name, record_value, expires_at}}`. Same key + hostname again = 200 with the existing row. 409 `hostname_taken` if another key or a user owns the hostname. Reject IPs. Rate limit per IP (e.g. 5/h) |
| `GET /directory/networks/lookup?hostname=` | 200 `{network, verification?}` (verification only while pending) or 404 `{"error":"not_found"}` for this key |
| `PATCH /directory/networks/{id}` | name, description, tags, languages, is_nsfw, website_url |
| `POST /directory/networks/{id}/verify` | strict check, same body and 409 as `/servers/{id}/verify`; 1 per 30 s |
| `POST /directory/networks/{id}/regenerate-token` | new token, back to pending |
| `DELETE /directory/networks/{id}` | unlist |

`website_url` (and an optional `auth_url` so boson clients can offer the
network's own sign-in) are new optional columns. Two cron details noted while
reading the backend: `verification_last_checked_at` is overwritten on every
check, so the 14-day lapse grace never triggers with a daily run, and the cron
verifies pending rows whose 72 h token has expired.

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
  `object-src 'none'`. `img-src` adds only `BOSON_CDN_URL`'s origin when
  profiles or the directory are on (avatars, server icons). There are no
  other third-party requests (fonts are bundled).
- Destructive actions confirm in a dialog; removing a sign-in provider or a
  directory listing and stopping/restarting a protected service need its
  name typed.

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
| `GET /api/audit?event=&actor=&target=&since=&until=&cursor=&limit=` | `event` exact or prefix with `*`; `since`/`until` RFC 3339 or a duration back from now (`24h`); `user_id` still works for `actor`. `{entries, next_cursor, server_filtered, events}` |
| `GET /api/overview` | services (incl. `irc`), certificates, metrics, `edge`, `irc`, `public_probes` |
| `GET /api/sso` | removed; see `/api/sso/providers` |
| `GET/PATCH /api/settings` | `{settings}`; 501 `upstream_unsupported` on older auth |
| `GET/POST /api/sso/providers`, `GET/PATCH/DELETE /api/sso/providers/{id}`, `POST …/{id}/test` | |
| `POST /api/users` | create; `{user, invite_sent}` |
| `POST /api/users/{id}/password-reset`, `/verification` | 204 |
| `GET /api/users/{id}/profile` | always 200: `{configured, profile?, servers?, unsupported?, error?}` |
| `GET /api/services`, `GET /api/services/{name}` | containers of `DOCKER_PROJECT` |
| `POST /api/services/{name}/{start\|stop\|restart}` `{confirm}` | 428 `confirmation_required` for a protected service without its name; 202 for the panel itself |
| `GET /api/services/{name}/logs?tail=&since=&download=1` | `{lines, last_at}` or a text/plain attachment |
| `GET /api/directory?hostname=`, `POST /api/directory`, `PATCH /api/directory/{id}?hostname=`, `POST …/{id}/verify`, `POST …/{id}/regenerate-token`, `DELETE …/{id}` | |

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
| `admin_service_actions_total` | `action` (`start`, `stop`, `restart`), `outcome` (`success`, `refused`, `error`) |

`admin_upstream_*` covers `auth`, `docker`, `boson_api` and `directory`.

## Layout

```text
cmd/main.go                    → internal/commands (cobra: serve, version)
config/                        env config (configor)
http/server.go                 dependency wiring + listeners
http/handlers                  /api handlers, session/CSRF wrapper, SPA server
http/middleware                client IP, logging/metrics, security headers
internal/services/admin        sign-in, token refresh (single-flight), admin operations
internal/services/overview     health, versions, certificates, PromQL stats
internal/services/containers   services control (Docker through the socket proxy)
internal/services/directory    boson.chat directory status and registration
internal/clients/{authapi,health,tlsprobe,ircprobe,promql,dockerapi,bosonapi}   upstream clients behind interfaces
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
