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
  certificates, each service (see "Which services" below) (up / degraded / down, latency,
  the failing check's status and body), IRC from `IRC_CHECK` (up / down,
  handshake latency and the Ergo version), their versions, the
  certificates in `TLS_CHECK_HOSTS` or `CERT_HOSTS` (valid, renews soon, expiring, expired,
  untrusted or unreachable; days left; issuer), and, when `METRICS_URL` is set,
  live numbers with one-hour trends: QUIC connections, live rooms,
  participants, audio frames forwarded per second, logins in the last hour by
  outcome, refresh-token reuse (24 h) and rate-limited requests (1 h). Without
  `METRICS_URL` it says metrics are off. A Grafana link appears when
  `GRAFANA_URL` is set. Refreshes every 15 s while the tab is visible.
  Each row shows two latencies: **internal** (the panel's own check over the
  container network) and **from you** (timed by your browser against the
  service's public endpoint; see below).
- **Services:** every container of the compose project with its state, uptime,
  image version, restarts, CPU and memory; restart, stop and start (with a
  confirmation, and the service name typed for the panel itself and the
  edge); a log viewer per service (tail 100–5,000 lines, follow, filter,
  stderr only, download). Through a locked-down Docker socket proxy; see
  "Services control" below.
- **Metrics:** the VPS's health from node-exporter (scrape job `node`) and
  every scrape target's up/down, read from `METRICS_URL`. Tiles for uptime,
  CPU, memory, swap, root disk and load against the core count, with the
  thresholds the small box needs flagged in words and colour (memory ≥ 80 %
  high / ≥ 90 % critical, any swap in use, disk ≥ 85 % / ≥ 95 %, CPU ≥ 80 %,
  load above the cores) and PSI pressure when the kernel reports it. Charts
  over 1 h, 6 h, 24 h or 7 d (crosshair tooltips, arrow keys, a table view):
  CPU by mode (stacked), memory and swap used, load 1/5/15 with the cores,
  disk used per mount, disk read/write and network in/out for the two
  busiest interfaces. A filesystem table, and service health per scrape
  target (state, last scrape, last time up, availability over the range).
  Refreshes every 30 s while the tab is visible. Without node-exporter it
  says how to turn it on (the stack's `metrics` profile) and still shows the
  service health. The browser never sends PromQL: the BFF runs a fixed set
  of queries (cached 10 s).
- **Network settings:** registration (open, invite only, closed), required
  email verification, and who may create text channels and voice rooms. With
  auth ≥ 1.7.0 also channel registration: register new channels to their
  creator automatically, and unregister channels idle for a chosen number of
  days (never, presets or 1–3,650). On older auth that group is disabled with
  a note and its fields are never sent. With auth ≥ 1.8.0 also guest access:
  whether people can use IRC without an account (off = IRC requires a login);
  disabled with a note on older auth. With auth ≥ 1.18.0 also guest voice &
  video (off by default): whether guests can join voice rooms, and so video,
  under their nickname with a guest badge; disabled while guest access is
  off, and disabled with a note on older auth.
- **Nick registration:** the api's nick claims (registering users' nicks
  with NickServ on other IRC networks through `reg+<user id>-<token>@domain`
  addresses and a POP3 mailbox; off by default). The switch, email domain,
  claims per user per hour and the mailbox (host, port, TLS, user) are
  `nick_claim.*` keys of the config file (`settings.manage`); the password
  stays in the api's `.env` (`NICK_CLAIM_POP3_PASSWORD`) and the page shows
  whether it's set. **Test connection** has the api sign in and count
  messages with the unsaved values; a status card shows the worker (running,
  last check, last OK, last error, failures in a row) and pending claims.
  Turning it on without a domain and a mail server is refused. Needs
  `BOSON_API_INTERNAL_URL`. Browser checks: `./scripts/e2e-nick.sh`.
- **Uploads:** the files service (files people attach in the boson app,
  with a rolling quota; off by default). The switch, how long files are
  kept and the quota window (`72h`, `3d`), account and guest quota and
  largest file (`5 GB`, `100MB`; as the files service reads them, KB/MB/GB
  are powers of 1000 and KiB/MiB/GiB powers of 1024; saved as `5GB`), guest
  uploads on/off, the free space to keep (`storage.min_free`) and, for S3,
  a capacity budget (`storage.capacity`), and the storage driver (local
  disk or S3: endpoint, bucket, region, path-style, optional public base
  URL; these apply after a restart of files, which the page says) are
  `files.*` keys of the config file (`settings.manage`). S3 keys stay in `.env`
  (`FILES_S3_ACCESS_KEY` / `FILES_S3_SECRET_KEY`). S3 without an endpoint
  and bucket, or a largest file above its quota, is refused. A status card
  shows storage health (healthy, full, failing), stored bytes, free space,
  totals, recent errors, settings waiting for a restart and the limits the
  service runs with, polled every 15 s. Needs `BOSON_FILES_INTERNAL_URL`.
- **Uploaded files:** moderation (`users.moderate` or `users.manage`):
  search by file name or id, filter by uploader and state (live, all,
  deleted, expired, uploading), open, delete (with an optional reason;
  logged with the actor and sent as `X-Acting-User`, shown as "deleted by").
  A user's page shows their usage of the quota and when space frees up
  (`users.view`), and sets or clears a per-user quota override
  (`settings.manage`).
  Browser checks: `./scripts/e2e-files.sh`.
- **Directory:** whether the network is listed in the boson directory
  (directory.boson.chat), and listing it with a network key and a DNS TXT
  proof. With the stack's own directory: its name/operator/contact and sync
  interval, the "Include the boson main directory" switch, and its peers
  (add, enable/disable, sync, remove), all without editing `.env`. See
  "Directory" below.
- **Users:** search by username or email, paginate, sort by last active
  (the "Last active" column: "3h ago", full time on hover), create a user (with a
  password or an emailed set-password link), open a user to change their
  role, disable or enable them (disabling signs them out everywhere), send a
  password reset or a new verification email, see their linked SSO sign-ins,
  their boson profile (avatar, display name, handle) and the networks they
  list in the directory (from the stack's own directory when it runs one), when they last
  signed in (method and IP) and were last active, list and revoke their sessions, and see their recent
  activity. Last signed in / last active need an auth release that reports
  them; with an older one the column and fields are hidden.
  Browser checks: `./scripts/e2e-users.sh`.
- **Roles:** network-wide, IRC-native roles (auth ≥ 1.5.0). Each role is
  an Ergo oper class with a WHOIS line and an optional vhost, so IRC users see
  it in `/whois` and in hostmasks. Built-ins Owner, Admin, Moderator and
  Member (everyone) can't be deleted; add your own. A Discord-style list
  (drag or ↑ ↓ to reorder, colour, member count) and an editor: **Display**
  (name, colour picker, hoist, mentionable, delete with the name typed),
  **Permissions** (grouped switches with descriptions), **IRC** (oper,
  WHOIS line and vhost template with a live mIRC-style `/whois` and
  `nick!user@host` preview) and **Members** (search to add, remove). Users
  show role chips; a user's page assigns roles (multi-select) and lists
  their effective permissions. You can only change roles below your own
  highest role and grant permissions you have; Owner bypasses that. The
  auth service enforces it, the panel disables what you can't do.
- **Role channels and Channel access** (auth ≥ 1.16.0, `roles.manage`): a
  role's **Channels** tab (Member's is "everyone") adds and removes `#channels`,
  with an auto-join box each, a private/public badge and a preview ("Members
  of Mods will be joined to #mods and #staff"). The **Channel access** page
  marks channels private (only the roles that list them, plus IRC operators,
  can join), shows who gets joined where, and holds the auto-join switches:
  `roles.channels.autojoin` and `roles.channels.rejoin` (`login` | `once`),
  config keys that need `settings.manage`. auth audits the writes
  (`role_channels_updated`, `channel_access_updated`) and the panel logs them.
  Browser checks: `./scripts/e2e-channels.sh`.
- **Invites:** create (uses, expiry, note), with the code shown once; list with
  status (active, used up, expired, revoked); revoke. Warns when registration
  is open or closed rather than invite-only.
- **Audit log:** filter by event (or group: admin actions, roles, sign-ins,
  SSO, password events), who did it, the affected account, and a time window;
  scrolls through the whole history page by page. Usernames are resolved for
  the actor and for the account an admin action affected.
- **Sign-in providers:** add, edit, test, switch on/off and remove OpenID
  Connect and SAML providers, with the callback / ACS / SP metadata URLs to
  copy and setup steps for Google, Authentik, Zitadel, Keycloak, Okta and
  Microsoft Entra ID. Client secrets are write-only.

Every page needs a permission (see "Permissions" below): the navigation
only lists the pages your roles allow, and the BFF refuses the rest.

Features whose upstream endpoint doesn't exist yet (an older auth release,
a boson API without the admin profile read) show what they need ("needs
auth ≥ v1.4.0") instead of an error; see "Upstream work".

IRC isn't managed from the panel, but it is monitored: with `IRC_CHECK` set
the panel connects to the IRC server (Ergo in the stack) every overview poll
(cached for 30 s), sends `CAP LS 302`, and reports it up once the server
answers. It then registers with a throwaway nick (`bosonadm…`), reads the
version from `RPL_MYINFO` (004) and quits cleanly; if registration doesn't
finish in time, or the server requires a login because guest access is off
(Ergo's `FAIL * ACCOUNT_REQUIRED`), the server still counts as up, just
without a version. The TLS
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

Every origin in `PUBLIC_PROBE_TARGETS`, and with the socket proxy on every
built-in public endpoint whose hostname is set (below), is added to the CSP's
`connect-src`; nothing else is.

### Which services

With the Docker socket proxy on (`DOCKER_PROXY_URL`), the stack view shows
the compose services of `DOCKER_PROJECT` that have a container (running or
not; a stopped one shows as down), so an optional profile that is off doesn't
show up as down. The panel knows how to probe the stack's services:

| Service | Internal health | Version | From you |
|---|---|---|---|
| `auth` | `auth:8081/healthz`, `/readyz` | `auth:8081/metrics` | `https://$AUTH_HOST/healthz` |
| `quicvoiced` | `quicvoiced:8080/healthz` | `quicvoiced:8080/version` | `https://$VOICE_HOST/healthz` |
| IRC (`ergo`) | `IRC_CHECK` | the IRC handshake | `wss://$IRC_HOST/webirc` |
| `api` | `api:3000/health` | image tag | `https://$API_HOST/health` |
| `directory` | `directory:8081/healthz` | `directory:8081/metrics` | `https://$DIRECTORY_HOST/healthz` |
| `files` | `files:3001/healthz` | `files:3001/metrics` | `https://$FILES_HOST/healthz` |
| `web` (the web client) | `web:8080/healthz` | `web:8080/boson-config.js` (`version: "…"`) | `https://$WEB_HOST/healthz` |
| `webircgateway` (its IRC gateway) | `webircgateway:8080/webirc/info` | image tag | `https://$WEB_HOST/webirc/info` |
| `victoriametrics` | `victoriametrics:8428/health` | image | – |
| `minio` | `minio:9000/minio/health/live` | image | – |

A version endpoint that fails falls back to the container image's version
(its `org.opencontainers.image.version` label, else its tag). A public probe
is only listed when its `*_HOST` is set. `HEALTH_TARGETS`,
`VERSION_TARGETS` and `PUBLIC_PROBE_TARGETS` still apply on top: a name
from the table replaces that service's built-in checks (or version source,
or public endpoint), any other name is added. Names from the table are
hidden while their container doesn't exist; other names are always shown.
If the proxy can't be reached, the last listing stands in for 5 minutes,
then the env lists alone are used, as they are with the proxy off.

## Running locally

Needs Docker, Go 1.27, Node 24+, and the `auth` and `quicvoice` repos checked
out next to this one (`../auth`, `../quicvoice`).

```sh
make dev-env      # writes .env with random dev secrets
make dev-up       # postgres, mailpit, auth, admin, caddy, ergo, quicvoiced, victoriametrics
docker compose exec auth ./auth user create --username admin --email admin@example.com
docker compose exec auth ./auth user set-role admin admin   # auth ≥ 1.5.0: gives the Admin role
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

The panel's own settings are environment variables. Durations use Go syntax
(`15m`, `12h`). Three of them are only defaults when the stack config file
is set up (below): `DIRECTORY_REGISTRATION`, `DIRECTORY_HOSTNAME` and
`TLS_CHECK_HOSTS`. Everything else (URLs of the services, Docker, metrics,
health targets, IRC check, secrets) is a deployment setting: change it in the
stack's `.env` and recreate the service.

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
| `TLS_CHECK_HOSTS` | from `CERT_HOSTS` | *default*: `admin.tls_check_hosts` in the config file wins. `host:port[@dial-host:dial-port],…`. The name is sent as SNI and verified; the optional dial address lets you check a public name over the internal network. Results are cached 5 minutes |
| `CERT_HOSTS` | – | the stack's certificate hostnames (`a.example.com,b.example.com`). With `TLS_CHECK_HOSTS` empty each is checked as `host:443@TLS_EDGE_ADDR`, and the IRC host (`IRC_HOST`, else `IRC_CHECK_SERVER_NAME`) as `host:6697` (`IRC_CHECK`'s port), dialled directly |
| `TLS_EDGE_ADDR` | – | where `CERT_HOSTS`' 443 checks are dialled, e.g. `traefik:443`. Empty = each hostname itself |
| `AUTH_HOST`, `VOICE_HOST`, `IRC_HOST`, `API_HOST`, `DIRECTORY_HOST`, `FILES_HOST`, `VIDEO_HOST`, `WEB_HOST` | – | the stack's public hostnames: the built-in "from you" probes (with `DOCKER_PROXY_URL`; see "Which services") |
| `IRC_CHECK` | – | IRC server to probe: `ircs://host[:6697]` (TLS) or `irc://host[:6667]`, e.g. `ircs://ergo:6697`. Empty = IRC "not configured" |
| `IRC_CHECK_SERVER_NAME` | `IRC_CHECK`'s host | TLS SNI for the probe, e.g. the public `IRC_HOST` when dialling the container |
| `PUBLIC_PROBE_TARGETS` | – | `name=url,…` public endpoints your browser times (`https://`, `http://`, `wss://`, `ws://`). Names match services (`auth`, `api`, `quicvoiced`, `irc`); `admin` overrides the panel's own `/healthz`. Origins are added to the CSP `connect-src` |
| `EDGE_NAME` | `Traefik` | what the stack view calls the reverse proxy in front of everything |
| `UPSTREAM_TIMEOUT` | `5s` | per upstream request (health and TLS checks use at most 3 s / 5 s) |
| `DOCKER_PROXY_URL` | – | the Docker socket proxy, e.g. `http://docker-proxy:2375`. Empty = services control off, and the stack view probes the env lists alone. A `unix:` URL or a `docker.sock` path is refused |
| `DOCKER_PROJECT` | `boson` | compose project whose containers are shown and controlled; nothing outside it is touched |
| `DOCKER_PROTECTED_SERVICES` | `admin,traefik` | compose services whose name must be typed to stop or restart them (the panel's own container always is) |
| `DOCKER_TIMEOUT` | `20s` | per Docker call (stats take about a second) |
| `BOSON_API_URL` | – | the stack's boson API (`http://api:3000`), for users' boson profiles. Empty = profiles off |
| `BOSON_API_INTERNAL_URL` | – | the api's internal listener (`http://api:3001`): the Nick registration page's status and Test connection. Empty = the page says the api isn't reachable (settings still save to the file) |
| `BOSON_API_INTERNAL_TOKEN` | – | the api's `INTERNAL_API_TOKEN`, sent as `X-Internal-Token` |
| `BOSON_FILES_INTERNAL_URL` | – | the files service's internal listener (`http://files:3001`): the Uploads page's status, Uploaded files and users' usage. Empty = those say it isn't reachable (settings still save to the file) |
| `BOSON_FILES_INTERNAL_TOKEN` | – | the files service's `INTERNAL_TOKEN` (stack: `FILES_INTERNAL_TOKEN`), sent as `X-Internal-Token` |
| `BOSON_CDN_URL` | `https://cdn.boson.chat` | where avatars and server icons are served from; added to the CSP `img-src` |
| `DIRECTORY_API_URL` | `https://directory.boson.chat` | the boson directory this network is listed in ([boson-chat/directory](https://github.com/boson-chat/directory), `/v1` API). Empty = the directory page is off |
| `DIRECTORY_REGISTRATION` | `true` | *default*: `admin.directory_registration` in the config file wins. Register and edit this network's listing there (network key + DNS proof). `false` = the page only reports whether it is listed |
| `DIRECTORY_LOCAL_URL` | – | the stack's own directory (`directory` profile), e.g. `http://directory:8080`: its settings, the "Include the boson main directory" switch, its peers, and the listings each user owns. Empty = off |
| `DIRECTORY_INTERNAL_TOKEN` | – | operator token for `DIRECTORY_LOCAL_URL` (the directory's `DIRECTORY_INTERNAL_TOKEN`); required with it |
| `DIRECTORY_KEY` | `SESSION_KEY` | secret the directory management key is derived from. Set it if you rotate `SESSION_KEY`, or the panel loses edit access to its listing |
| `DIRECTORY_HOSTNAME` | `IRC_CHECK_SERVER_NAME` | *default*: `admin.directory_hostname` in the config file wins. The IRC hostname to list |
| `BOSON_CONFIG` | – | the stack config file, e.g. `/config/boson.yaml` (see below). Its directory must be writable: saves are atomic renames. Empty = everything from the env, as before |
| `CONFIG_STATUS_TARGETS` | – | `name=url,…` of each service's `GET /internal/config/status`; names are compose services (the Restart button uses them), e.g. `auth=http://auth:8081/internal/config/status,ergo=http://ergo:8098/internal/config/status` |
| `BOSON_CONFIG_POLL` | `10s` | how often the file is re-read when no change event arrives |
| `EMAILS_DIR` | `emails/` next to `BOSON_CONFIG` | where the Emails page writes the MJML sources and compiled HTML (`emails/<part>-<hash>.{mjml,html}`, named by `auth.email.layout` and `auth.email.templates.*.{mjml,html}`). Auth reads the same directory (`BOSON_EMAILS_DIR`) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | – | enable OTLP/HTTP metrics push in addition to `/metrics` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` / `OTEL_EXPORTER_OTLP_METRICS_PROTOCOL` | `http/protobuf` | only `http/protobuf` is supported |
| `OTEL_METRICS_EXPORTER` | – | `none` turns OTLP push off |

The live numbers assume the metrics backend scrapes the services' Prometheus
endpoints (`auth:8081/metrics`, `quicvoiced:8080/metrics`), or ingests OTLP
with Prometheus naming, so the series are named as in the
[quicvoice](https://github.com/boson-chat/quicvoice#metrics) and
[auth](https://github.com/boson-chat/auth#metrics) READMEs.

### The stack config file

With `BOSON_CONFIG` the panel reads and writes the stack's one config file,
`config/boson.yaml` (schema v1; the stack repo's `docs/config.md`): the
**Stack config** page edits the panel's own section (`admin:` directory
registration, the hostname to list, certificates to watch), voice rooms and
limits, IRC options, the auth service's email and CORS settings and the
network name; **Network settings** and the stack directory's settings on
**Directory** save to it too instead of to auth's and the directory's
databases. A value in the file wins over the env, which stays the default;
**Reset to default** removes the key.

- **Edits keep the file readable.** The panel edits it through the yaml.v3
  Node API: comments, key order and keys it doesn't know survive, and it notes
  `# last saved in the admin panel by <user> at <time>` on the `version:` line.
  Writes are atomic (temp file in the same directory, fsync, rename, fsync of
  the directory) and keep the file's mode (0644 for a new one; no secrets go
  in it).
- **Hand edits are picked up live:** fsnotify on the directory (a save by
  rename replaces the inode) plus a poll every `BOSON_CONFIG_POLL`. A file
  that doesn't parse is never used or overwritten: the last good version stays
  in effect, the log says `config file has an error`, and every page shows a
  banner with the line until someone fixes it. A value that parses but isn't
  valid (say `directory_hostname: 203.0.113.9`) keeps that key's last good
  value and is listed with its line.
- **Several admins and tabs:** saves are serialised and re-read the file
  first, so concurrent edits of different keys all land (last write wins for
  the same key). `PATCH /api/config` takes an optional `If-Match: <ETag>`
  (`412 config_changed` when the file moved on); the Stack config page sends it.
- **Who may change what:** reading needs `admin.panel` (nothing secret is in
  the file); each key needs its own permission: `network.*`, `auth.*` and
  `voice.*` → `settings.manage`; `directory.*`, `admin.directory_registration`
  and `admin.directory_hostname` → `directory.manage`; `irc.*` and
  `admin.tls_check_hosts` → `services.manage` (certificates and IRC listeners
  are operations, like restarting a service). `irc.plaintext_port` is left to
  hand edits: compose publishes the container's port.
- **Did it apply?** The panel asks each `CONFIG_STATUS_TARGETS` service for
  `/internal/config/status` (and serves its own on `INTERNAL_ADDR`) and shows
  applied / loading / needs a restart for `<keys>` / rejected the file, with
  a Restart button (through the Docker socket proxy, typed confirmation for
  `admin` and `traefik`).
- Every save is logged (`config file change`, actor, keys, old → new) and
  counted in `admin_settings_changes_total`.

For migrating an existing install, `admin export-config` prints the `admin:`
section from the env vars that are set, and `admin config merge [--overwrite]
FILE` merges YAML from stdin (each service's `export-config`) into the file,
keeping keys it already has; the stack's `scripts/migrate-config.sh` runs both.

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
      # Without the socket proxy (below), list what to probe:
      HEALTH_TARGETS: >-
        auth=http://auth:8081/healthz,auth=http://auth:8081/readyz,
        quicvoiced=http://quicvoiced:8080/healthz
      VERSION_TARGETS: auth=http://auth:8081/metrics,quicvoiced=http://quicvoiced:8080/version
      PUBLIC_PROBE_TARGETS: >-
        auth=https://${AUTH_HOST}/healthz,api=https://${API_HOST}/health,
        quicvoiced=https://${VOICE_HOST}/healthz,irc=wss://${IRC_HOST}/webirc
      # Certificates: every hostname, 443 through the edge, IRC on 6697.
      CERT_HOSTS: ${CERT_HOSTS}
      TLS_EDGE_ADDR: traefik:443
      IRC_CHECK: ircs://ergo:6697                 # irc profile; leave unset without it
      IRC_CHECK_SERVER_NAME: ${IRC_HOST}
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
      # The stack view then shows the services that have a container, with
      # built-in checks (HEALTH_TARGETS etc. can go) and these public probes:
      AUTH_HOST: ${AUTH_HOST}
      VOICE_HOST: ${VOICE_HOST}
      IRC_HOST: ${IRC_HOST}
      API_HOST: ${API_HOST}
      DIRECTORY_HOST: ${DIRECTORY_HOST}
      FILES_HOST: ${FILES_HOST}
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

The boson directory ([boson-chat/directory](https://github.com/boson-chat/directory),
`https://directory.boson.chat` by default, or any other one in
`DIRECTORY_API_URL`) lists IRC networks that proved control of their hostname
with a TXT record `_boson.<hostname>` = `boson-verify=<token>` (checked
against Cloudflare, Google and Quad9, and re-checked daily by the directory).
People list networks there with a boson account; a self-hosted network has
its own auth service, so the panel registers it as the network itself
instead, proving it twice: a **network key** (HMAC-SHA256 of
`DIRECTORY_KEY`/`SESSION_KEY` and the hostname, sent as
`Authorization: Network <key>`; the directory stores only its hash, so only
this panel can edit the listing and the panel stores nothing) and the **DNS
TXT record**. The page submits the listing, shows the record to add, runs the
check (with each resolver's answer), edits the listing, issues a new record
and removes it. A directory without the `/v1` API (an old boson API at
`api.boson.chat`) reads as "unsupported", and the page falls back to that
API's `GET /servers` search to say whether the network is listed.

**The stack's own directory.** With the stack's `directory` profile, set
`DIRECTORY_LOCAL_URL=http://directory:8080` and `DIRECTORY_INTERNAL_TOKEN`
(the stack does both). The Directory page then gets a "Your own directory"
section where everything is changed at runtime: the directory keeps these
settings in its own database (its env values are only defaults), so nobody
edits `.env` or recreates the service.

- **Include the boson main directory**: on by default, your directory also
  shows every network listed at directory.boson.chat, each only after your
  directory has checked its DNS itself. Works whether or not the built-in
  peer exists yet (directory ≥ 1.4.0 creates or re-enables it). When the
  stack's directory *is* the main directory (its main peer URL is its own,
  like directory.boson.chat), the page says so instead of showing the
  switch.
- **Directory details**: name, operator, contact (its
  `/.well-known/boson-directory.json`) and how often peers sync.
- **Other directories**: add one by URL (checked against its `.well-known`
  first), switch it on/off, sync now, remove; each shows its listing count,
  last sync and last error.

Settings need directory ≥ 1.4.0; on an older one the page says so and the
switch only toggles an existing built-in peer (peers work either way). A
user's page lists the networks they registered in that directory, in any
state. Every change needs `directory.manage` and is logged here
(`action=directory.include_main|settings|peer_add|peer_enable|peer_sync|peer_remove`,
actor) and in the directory's own audit log (the panel sends
`X-Acting-User`).

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
| `GET/PATCH /v1/admin/settings` add `channel_auto_register` (bool) and `channel_expiry_days` (0–3650, 0 = never) | 1.7.0 | Channel registration group. Absent on older auth: the panel leaves them out of its own JSON and never PATCHes them |
| `GET/PATCH /v1/admin/settings` add `guest_access` (bool; `false` = IRC requires a login) | 1.8.0 | Guest access toggle. Absent on older auth: same handling, the toggle is disabled with a note |
| `GET/PATCH /v1/admin/settings` add `guest_voice` (bool, default `false`; guests may join voice and video, only while `guest_access` is on) | 1.18.0 | Guest voice & video toggle (`auth.guest_voice` in the stack config file). Absent on older auth: same handling, the toggle is disabled with a note |
| `POST /v1/admin/users` `{username, email, role, password \| send_invite_email}` → 201 `{user, invite_email_sent}` | 1.3.0 | Create user |
| `POST /v1/admin/users/{id}/password-reset`, `…/verification` → 202 | 1.3.0 | Emails. `400 no_email`, `409 already_verified`, `409 account_disabled` (and `409 username_taken` / `email_taken` / `slug_taken`) get panel wording |
| `GET /v1/admin/audit?event=&actor=&target=&since=&until=&cursor=&limit=` → `{entries[{…, actor_user_id, actor_username, target_user_id, target_username}], next_cursor}` | 1.3.0 | Audit paging. `event` is an exact name or a comma list; the panel expands its `admin_*`-style groups into names. `next_cursor` is always present (`null` at the end), which is how the panel tells it from 1.2 |
| `GET /v1/admin/users/{id}` → `{user, identities[{id, provider_id, provider_slug, provider_kind, provider_display_name, subject, email, created_at, last_login_at}], has_password}` | 1.4.0 | Linked sign-ins, "SSO only" |
| `GET/POST /v1/admin/sso/providers`, `GET/PATCH/DELETE …/{id}`, `POST …/{id}/test` → `{ok, details{…}}` | 1.4.0 | Sign-in providers. `config` is one flat object per kind (OIDC: `issuer, client_id, client_secret (write-only), scopes, username_claim, email_claim, display_name_claim`; SAML: `idp_metadata_url \| idp_metadata_xml, entity_id, attribute_map, name_id_format, sign_requests`); the panel's own API nests it as `{oidc}` / `{saml}`. PATCH merges key by key: the panel omits `client_secret` to keep it and sends cleared optional keys as `null`. Responses carry `has_client_secret`, `start_url`, `redirect_uri` (OIDC), `acs_url`, `sp_metadata_url`, `sp_entity_id`, `sp_certificate` (SAML). Deleting a provider deletes its linked identities |

**auth 1.5.0: roles and permissions** ([docs/roles.md](https://github.com/boson-chat/auth/blob/main/docs/roles.md)).
Checked against a local auth built from the roles branch:
`AUTH_TEST_URL=http://127.0.0.1:8080 AUTH_TEST_OWNER=… AUTH_TEST_ADMIN=… AUTH_TEST_PASSWORD=… go test -run TestRolesAgainstAuth ./internal/clients/authapi/`
(an Owner and an Admin sharing a password).

| Endpoint | Used for |
|---|---|
| `GET /v1/me`, `GET /v1/admin/users[/{id}]` add `roles[{id, name, color, position}]` (never Member) and, on `/me` and a single user, effective `permissions[]` | access checks, chips, effective permissions. Their absence means auth < 1.5.0 |
| `GET /v1/admin/permissions` → `{permissions[{id, group, label, description}]}` | the Permissions tab, rendered from the live list (33 in 1.5.0) |
| `GET /v1/admin/roles` (most senior first), `GET/PATCH/DELETE /v1/admin/roles/{id}`, `POST /v1/admin/roles` → 201 | Roles page. `null` clears `color`, `irc.whois_line` (then `is a <name>`) and `irc.vhost`; `irc.oper` comes back effective (on with any `irc.*` permission). Built-ins can't be renamed or deleted (`409 builtin_role`) |
| `POST /v1/admin/roles/reorder {order}` → `{roles}` | the movable roles only (not Owner or Member), most senior first; they swap positions among themselves |
| `GET /v1/admin/roles/{id}/members?cursor=&limit=` → `{users[{id, username, display_name, disabled, assigned_at, assigned_by}], next_cursor}` | Members tab |
| `PUT /v1/admin/users/{id}/roles {role_ids}` → `{roles}` | assigning roles (the full set) |

auth enforces the hierarchy (roles and users strictly below your highest
role, only permissions you hold, Owner bypasses, `roles.owner` for the Owner
role, the last Owner stays) with `403 role_hierarchy` /
`permission_not_held` and `409 last_owner`; the panel disables the same
things and words those errors. auth does not check `services.*` or
`directory.manage`: the BFF does. Vhost templates and role names are
checked with auth's rules before saving (letters, digits, `. - _ /` and
`{username}`, ≤ 64 bytes with a 30-character username, no leading or
trailing `.`/`/`, no `..`/`//`; names 1–32 letters, digits, spaces, `-`,
`_`), and the preview renders `{username}` as auth does (ASCII lower case,
`` []\`^{|} `` → `-`). auth's built-in Moderator has no `admin.panel`, so
moderators don't open the panel unless a role gives it to them.

**boson API: `GET /v1/admin/profiles/{auth_user_id}`** (new). The backend
already verifies auth access tokens; the panel calls it with the signed-in
admin's own token. The backend must:

1. Gate on the token's `role` claim being `admin`, or with auth ≥ 1.5.0 on
   `users.view` (403 otherwise). Nothing checks `role` today.
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

**boson directory** ([boson-chat/directory](https://github.com/boson-chat/directory) `/v1`).
Network JSON as the panel reads it: `{id, hostname, port, tls, name,
description, tags, languages, is_nsfw, website_url, auth_url, icon_url,
banner_url, origin, status, health_status, user_count, last_checked_at,
last_verified_at, created_at}` (the backend's `verification_status` /
`registered_at` names are read too). The panel's own API keeps the old
names (`verification_status`, `verification_last_checked_at`,
`registered_at`) so the UI didn't change. A plain-text 404, a 405 or an
HTML page (the directory's web UI answers unknown paths) reads as "endpoint
missing". Errors are `{error, message, code}`. Checked against a real
directory (`ENV=development DNS_VERIFIER=fake`, an operator token), not only
against fakes; rerun with
`DIRECTORY_TEST_URL=http://127.0.0.1:8080 DIRECTORY_TEST_TOKEN=… go test -run TestAgainstDirectory ./internal/services/directory/`.

| Endpoint | Auth | |
|---|---|---|
| `GET /v1/networks/lookup?hostname=` | none | the verified public listing (`{network, verification: null}`, or a bare network), 404 `{"error":"not_found"}` |
| `GET /v1/networks/lookup?hostname=` | `Network <key>` | this key's own listing in any status: `{network, verification?}`; 404 `not_found` when the key has none |
| `POST /v1/networks` `{hostname, port, tls, name, description?, tags?, languages?, is_nsfw?, website_url?}` | `Network <key>` | 201 `{network, verification: {record_name, record_value, expires_at}}`; same key + hostname again = 200 with the existing row; 409 `hostname_taken` |
| `PATCH /v1/networks/{id}` | `Network <key>` | `{network, verification?}` |
| `POST /v1/networks/{id}/verify` | `Network <key>` | 200 `{success, network, report: {success, results: {cloudflare\|google\|quad9: {outcome, detail?, records?}}}}`, 409 with the same body when a resolver doesn't see the record |
| `POST /v1/networks/{id}/regenerate-token` | `Network <key>` | `{network, verification}`, back to pending |
| `DELETE /v1/networks/{id}` | `Network <key>` | 204 |
| `GET /v1/networks?owner=<auth_user_id>&limit=100&cursor=` | `X-Internal-Token` | `{networks, next_cursor}`, every status for that owner; the panel follows `next_cursor` (≤ 5 pages) |
| `GET /v1/admin/peers` | `X-Internal-Token` | `{peers: [{id, url, name, api, enabled, builtin, active, last_sync_at, last_error, listing_count, …}]}`; `builtin: true` is the main peer, absent with `DIRECTORY_INCLUDE_MAIN=false` |
| `PATCH /v1/admin/peers/{id}` `{enabled}` | `X-Internal-Token` (+ `X-Acting-User`) | `{peer}` (or the bare peer) |
| `GET /v1/admin/peers/check?url=` · `POST /v1/admin/peers {url}` · `DELETE /v1/admin/peers/{id}` · `POST /v1/admin/peers/{id}/sync` | `X-Internal-Token` (+ `X-Acting-User`) | check / add / remove / sync now (502 `peer_unreachable` is shown with its reason) |
| `GET /v1/admin/settings` · `PATCH /v1/admin/settings {name?, operator?, contact?, include_main?, sync_every?}` | `X-Internal-Token` (+ `X-Acting-User`) | directory ≥ 1.4.0: `{settings, defaults, overridden, is_main, main_active, main_peer, …}`; older directories read as unsupported |

## How it works

### Sessions

- Sign-in posts to the auth service's `/v1/login` in bearer mode, then reads
  the account's effective permissions from `/v1/me`. Only accounts with
  `admin.panel` get a panel session (on auth < 1.5.0: `role=admin`); anyone
  else's new upstream session is logged out straight away.
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
- Every `/api` call checks the account's permissions (below). They come from
  `/v1/me`, remembered per panel session for 10 s; `GET /api/session`, Docker
  and directory calls re-check every time. Losing `admin.panel` signs the
  account out on its next request; a role change made in the panel applies
  to the signed-in admin straight away. Admin API calls are also authorised
  by the auth service on every request.
- Signing out revokes the upstream session.

### Permissions

With auth ≥ 1.5.0 access comes from the account's roles. `admin.panel` opens
the panel; each page and endpoint needs one more permission on top
(`http/handlers/routes.go`). Without it the API answers
`403 missing_permission` (the session stays) and the UI hides the nav item,
the page and the buttons.

| Permission | Pages and endpoints |
|---|---|
| `admin.panel` | sign in, `/api/session`, reading the config file (`GET /api/config`, `/raw`, `/status`), reading `/api/permissions`, `/api/roles`, `/api/roles/{id}` and its members |
| `services.view` | Stack (`/api/overview`), Metrics (`/api/metrics/host`), Services list and logs (`GET /api/services…`) |
| `services.manage` | restart / stop / start (`POST /api/services/{name}/{action}`); config file keys `irc.*`, `admin.tls_check_hosts` |
| `settings.manage` | Network settings (`GET/PATCH /api/settings`); config file keys `network.*`, `auth.*`, `voice.*`, `nick_claim.*`, `files.*`; Uploads status (`GET /api/files/status`), a user's uploads quota override (`PUT/DELETE /api/users/{id}/files-quota`) |
| `directory.manage` | Directory (`/api/directory…`); config file keys `directory.*`, `admin.directory_*` |
| `audit.view` | Audit log (`/api/audit`) |
| `users.view` | Users, a user's page, sessions, activity, profile, uploads usage (`GET /api/users/{id}/files-usage`) |
| `users.manage` | create users, password reset / verification emails |
| `users.moderate` or `users.manage` | disable / enable, revoke sessions (auth also requires outranking the user); Uploaded files (`GET /api/files`, `DELETE /api/files/{id}`) |
| `roles.manage` | Roles page and role changes (`POST/PATCH/DELETE /api/roles…`, reorder, members), assigning roles (`PUT /api/users/{id}/roles`, the 1.x `PATCH /api/users/{id} {role}`) |
| `invites.manage` | Invites (`/api/invites…`) |
| `sso.manage` | Sign-in providers (`/api/sso/providers…`) |

`roles.owner` (edit and assign Owner) and the network (`channel.create`,
`voice.create`, `voice.manage_any`, `voice.bypass_locks`) and IRC (`irc.*`,
Ergo capabilities) permissions aren't used by the panel itself; they are
edited on the Roles page and enforced by auth, the API, quicvoice and Ergo.

**Older auth (< 1.5.0):** no roles. `role=admin` means every permission and
anything else none, so the panel works as before; the Roles page says it
needs auth ≥ 1.5.0 and users keep the User / Admin choice.

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
| `GET /api/session` | `{user, csrf_token, expires_at, features: {version, grafana_url, metrics, …, irc_host}, permissions, roles, roles_supported}` or 401 |
| `POST /api/login` | `{login, password}` → session (sets the cookie) |
| `POST /api/logout` | 204, clears the cookie |
| `GET /api/users?q=&sort=&limit=&offset=` | `{users, total, limit, offset, sort, activity_supported}`. `sort`: `created` (default) \| `last_active` \| `last_sign_in`, newest first. `activity_supported` is false on an auth that doesn't report `last_sign_in_at` / `last_sign_in_method` / `last_sign_in_ip` / `last_active_at` (and ignores the sort); `sort` echoes what was applied. Each user has `activity_tracked` the same way |
| `GET /api/users/{id}`, `PATCH /api/users/{id}` `{role}` | |
| `POST /api/users/{id}/disable`, `/enable` | |
| `GET /api/users/{id}/sessions` | marks the panel's own session `current` |
| `GET /api/users/{id}/audit?limit=` | entries the user performed |
| `DELETE /api/sessions/{id}` | revoke (not the panel's own session) |
| `GET /api/invites`, `POST /api/invites` `{max_uses, ttl, note}`, `DELETE /api/invites/{id}` | the code is only in the create response |
| `GET /api/audit?event=&actor=&target=&since=&until=&cursor=&limit=` | `event` exact or prefix with `*`; `since`/`until` RFC 3339 or a duration back from now (`24h`); `user_id` still works for `actor`. `{entries, next_cursor, server_filtered, events}` |
| `GET /api/overview` | services (incl. `irc`), certificates, metrics, `edge`, `irc`, `public_probes` |
| `GET /api/metrics/host?range=1h\|6h\|24h\|7d&instance=` | `{configured, error?, range, step, start, end, node_exporter, instances, instance, host?: {hostname, kernel, stats, filesystems, charts: [{key, title, unit, stacked, max, reference, series: [{name, points: [[t, v]]}], error?}]}, targets: [{job, instance, status: up\|down\|stale, last_scrape, last_up, availability}], targets_error?}`. 400 on another range or an instance that isn't a `node` target; `configured: false` without `METRICS_URL` |
| `GET /api/sso` | removed; see `/api/sso/providers` |
| `GET/PATCH /api/settings` | `{settings}`; 501 `upstream_unsupported` on older auth. With `BOSON_CONFIG` the PATCH writes `auth:` in the config file and both answer `{settings, config: {managed, sources: {key: file\|default}}}` |
| `GET /api/config` | `{enabled, path, etag, exists, modified_at, saved_by, saved_at, error?: {message, line, column}, issues, keys: [{key, kind, perm, services, env, restart, set, value, default, line, editable}]}`; `ETag` header |
| `PATCH /api/config` `{set: {key: value}, unset: [key]}` (`If-Match` optional) | the same view; 400 invalid value, 403 `missing_permission` per key, 412 `config_changed`, 409 `config_file_invalid` (the file on disk doesn't parse), 503 `config_file_off` |
| `GET /api/config/raw` · `GET /api/config/status` | the file as on disk · `{services: [{name, state: applied\|pending\|restart_required\|error\|unreachable\|unsupported, restart_required, error, sections}]}` |
| `GET/POST /api/sso/providers`, `GET/PATCH/DELETE /api/sso/providers/{id}`, `POST …/{id}/test` | |
| `POST /api/users` | create; `{user, invite_sent}` |
| `POST /api/users/{id}/password-reset`, `/verification` | 204 |
| `GET /api/users/{id}/profile` | always 200: `{configured, profile?, servers?, unsupported?, error?}` |
| `GET /api/services`, `GET /api/services/{name}` | containers of `DOCKER_PROJECT` |
| `POST /api/services/{name}/{start\|stop\|restart}` `{confirm}` | 428 `confirmation_required` for a protected service without its name; 202 for the panel itself |
| `GET /api/services/{name}/logs?tail=&since=&download=1` | `{lines, last_at}` or a text/plain attachment |
| `GET /api/permissions` | `{permissions[{id, group, label, description}], source}`; `source: "panel"` (the panel's own list) on auth < 1.5.0 |
| `GET/POST /api/roles`, `GET/PATCH/DELETE /api/roles/{id}`, `POST /api/roles/reorder` `{order}` | roles, most senior first; 501 `upstream_unsupported` on auth < 1.5.0. `null` or `""` clears `color`, `irc.whois_line`, `irc.vhost` |
| `GET /api/roles/{id}/members?cursor=&limit=`, `PUT/DELETE /api/roles/{id}/members/{user_id}` | `{users, next_cursor}`; adding / removing one member reads and rewrites that user's roles |
| `PUT /api/users/{id}/roles` `{role_ids}` | `{roles}` |
| `GET /api/directory?hostname=`, `POST /api/directory`, `PATCH /api/directory/{id}?hostname=`, `POST …/{id}/verify`, `POST …/{id}/regenerate-token`, `DELETE …/{id}` | |
| `GET /api/directory/federation` | `{url, main_peer?, peers, settings?, settings_unsupported?, settings_error?, unsupported?, error?}`; 503 `directory_local_off` without `DIRECTORY_LOCAL_URL` |
| `PUT /api/directory/federation/include-main` `{enabled}` | the same; through the directory's settings (≥ 1.4.0), else by toggling the built-in peer (409 `main_peer_missing` when an old directory has none); 409 `is_main_directory` on the main directory |
| `PATCH /api/directory/settings` `{name?, operator?, contact?, sync_every?}` | federation JSON; unsupported before directory 1.4.0. With `BOSON_CONFIG` it (and include-main) writes `directory:` in the config file |
| `GET /api/directory/peers/check?url=` | `{url, well_known: {name, api, …}}` (a bare host gets `https://`) |
| `POST /api/directory/peers` `{url}` · `PATCH /api/directory/peers/{id}` `{enabled}` · `DELETE /api/directory/peers/{id}` | federation JSON (201 for add) |
| `POST /api/directory/peers/{id}/sync` | `{federation, result: {created, updated, deleted, verified, error?}}` |
| `GET /api/users/{id}/networks` | always 200: `{configured, networks?, unsupported?, error?}` (the stack's own directory) |

### Metrics

On `INTERNAL_ADDR` `/metrics` (and OTLP if configured), plus Go runtime metrics:

| Metric | Labels |
|---|---|
| `admin_http_requests_total` | `http_route` (mux pattern), `status_class` |
| `admin_http_duration_seconds` | `http_route` |
| `admin_upstream_requests_total` | `upstream`, `op`, `outcome` (status class or `error`) |
| `admin_upstream_duration_seconds` | `upstream` |
| `admin_logins_total` | `outcome` (`success`, `invalid_credentials`, `not_admin` (no `admin.panel`), `rate_limited`, `error`) |
| `admin_token_refreshes_total` | `outcome` (`success`, `shared`, `expired`, `error`) |
| `admin_service_actions_total` | `action` (`start`, `stop`, `restart`), `outcome` (`success`, `refused`, `error`) |
| `admin_settings_changes_total` | `section` (`admin`, `auth`, `voice`, …), `outcome` (`success`, `invalid`, `conflict`, `invalid_file`, `error`) |

`admin_upstream_*` covers `auth`, `docker`, `boson_api`, `directory` and `directory_local`.

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
http/handlers/routes.go        the permission each /api route needs
internal/services/admin/access.go   permission catalog, access checks and their cache
ui/src/lib/perms.ts            page permissions and the role hierarchy, as the UI applies them
ui/e2e/                        Playwright checks (dropdown keyboard/mouse, browser latency)
```

## Release

Conventional commits on `main` → semantic-release tags `vX.Y.Z` and pushes
`harbor.floret.dev/boson/admin:X.Y.Z` and `:latest` (linux/amd64 + arm64).
The workflow needs the repo secrets `HARBOR_USERNAME` / `HARBOR_PASSWORD`;
the Harbor `boson` project is public, so pulls need no credentials.

## License

Copyright (C) 2026 bludot / the boson authors

Licensed under the [GNU Affero General Public License v3.0](LICENSE) only (`AGPL-3.0-only`).

In plain English: if you run a modified version of this software as a network service, you must offer its source code to the people who use that service.
