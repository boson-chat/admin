#!/bin/sh
# The Uploads and Uploaded files pages' browser checks, self-contained:
# starts the fake auth (ui/e2e/fake-auth.mjs), the fake files service
# internal listener (ui/e2e/fake-files.mjs) and the panel on both with a
# throwaway config file, runs ui/e2e/uploads.spec.ts, and stops everything.
#
#   ./scripts/e2e-files.sh          # SCREENSHOT_DIR=... to keep screenshots
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
PIDS=""
trap 'for p in $PIDS; do kill "$p" 2>/dev/null || true; done; rm -rf "$TMP"' EXIT

(cd "$ROOT/ui" && npm run build >/dev/null)
(cd "$ROOT" && go build -o "$TMP/admin" ./cmd)
printf 'version: 1\nnetwork:\n  name: Example Chat\n' > "$TMP/boson.yaml"

node "$ROOT/ui/e2e/fake-auth.mjs" > "$TMP/fake-auth.log" 2>&1 &
PIDS="$PIDS $!"
FAKE_FILES_CONFIG="$TMP/boson.yaml" node "$ROOT/ui/e2e/fake-files.mjs" > "$TMP/fake-files.log" 2>&1 &
PIDS="$PIDS $!"
ENV=development HTTP_ADDR=127.0.0.1:18690 INTERNAL_ADDR=127.0.0.1:18691 \
	PUBLIC_URL=http://localhost:18690 COOKIE_SECURE=false \
	SESSION_KEY=$(openssl rand -base64 32) \
	AUTH_URL=http://127.0.0.1:18680 AUTH_INTERNAL_URL=http://127.0.0.1:18681 INTERNAL_API_TOKEN=e2e \
	BOSON_CONFIG="$TMP/boson.yaml" BOSON_CONFIG_POLL=1s DIRECTORY_REGISTRATION=false \
	BOSON_FILES_INTERNAL_URL=http://127.0.0.1:18683 BOSON_FILES_INTERNAL_TOKEN=e2e-files \
	CONFIG_STATUS_TARGETS=files=http://127.0.0.1:18683/internal/config/status \
	"$TMP/admin" serve > "$TMP/admin.log" 2>&1 &
PIDS="$PIDS $!"
for _ in 1 2 3 4 5 6 7 8 9 10; do
	curl -s -o /dev/null http://127.0.0.1:18690/api/session && break
	sleep 1
done

cd "$ROOT/ui"
BASE_URL=http://localhost:18690 FAKE_FILES_URL=http://127.0.0.1:18683 ADMIN_PASSWORD=e2e-password \
	npx playwright test e2e/auth.setup e2e/uploads.spec "$@"
