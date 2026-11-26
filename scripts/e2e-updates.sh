#!/bin/sh
# The Updates page's browser checks, self-contained: starts the fake auth
# (ui/e2e/fake-auth.mjs), the fake Boson host (dev/fakestack: GitHub
# releases, the image registry, the Docker API behind the socket proxy and
# the services' endpoints) and the panel on them, runs
# ui/e2e/updates.spec.ts (update available → changelog across versions →
# apply → checks → done; a breaking, migrating update that fails → rollback
# with the data restored → healthy), and stops everything.
#
#   ./scripts/e2e-updates.sh          # SCREENSHOT_DIR=... to keep screenshots
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
PIDS=""
trap 'for p in $PIDS; do kill "$p" 2>/dev/null || true; done; rm -rf "$TMP"' EXIT

(cd "$ROOT/ui" && npm run build >/dev/null)
(cd "$ROOT" && go build -o "$TMP/admin" ./cmd && go build -o "$TMP/fakestack" ./dev/fakestack)

F=http://127.0.0.1:18700
node "$ROOT/ui/e2e/fake-auth.mjs" > "$TMP/fake-auth.log" 2>&1 &
PIDS="$PIDS $!"
"$TMP/fakestack" -addr 127.0.0.1:18700 > "$TMP/fakestack.log" 2>&1 &
PIDS="$PIDS $!"
checks=""
for c in auth=auth api=api web=web web-config=web voice=quicvoiced video=quicvideod files=files directory=directory webircgateway=webircgateway; do
	checks="$checks${checks:+,}${c%%=*}=$F/check/${c#*=}"
done
ENV=development VERSION=1.26.0 HTTP_ADDR=127.0.0.1:18690 INTERNAL_ADDR=127.0.0.1:18691 \
	PUBLIC_URL=http://localhost:18690 COOKIE_SECURE=false \
	SESSION_KEY=$(openssl rand -base64 32) \
	AUTH_URL=http://127.0.0.1:18680 DIRECTORY_REGISTRATION=false \
	DOCKER_PROXY_URL=$F/docker UPDATES_DOCKER_PROXY_URL=$F/docker \
	UPDATES_GITHUB_API_URL=$F/gh UPDATES_REGISTRY_URL=$F UPDATES_STATE_DIR="$TMP/state" \
	UPDATES_BACKUP_DIR=/root/backups/boson UPDATES_HEALTH_TIMEOUT=4s UPDATES_CHECK_TARGETS="$checks" \
	"$TMP/admin" serve > "$TMP/admin.log" 2>&1 &
PIDS="$PIDS $!"
for _ in 1 2 3 4 5 6 7 8 9 10; do
	curl -s -o /dev/null http://127.0.0.1:18690/api/session && break
	sleep 1
done

cd "$ROOT/ui"
BASE_URL=http://localhost:18690 FAKE_STACK_URL=$F ADMIN_PASSWORD=e2e-password \
	npx playwright test e2e/auth.setup e2e/updates.spec "$@" || { tail -30 "$TMP/admin.log"; exit 1; }
