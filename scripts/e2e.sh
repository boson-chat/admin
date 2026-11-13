#!/bin/sh
# End-to-end check through the panel's API against the running dev compose
# (make dev-up). Needs curl and jq.
#
#   ADMIN_USER=admin ADMIN_PASSWORD_FILE=/path/to/pw ./scripts/e2e.sh
#
# Signs in, lists users, disables and re-enables a non-admin user, creates
# and revokes an invite, checks the audit log and the stack overview, signs out.
set -eu

BASE=${PANEL_URL:-http://localhost:8090}
USER=${ADMIN_USER:-admin}
PWFILE=${ADMIN_PASSWORD_FILE:?set ADMIN_PASSWORD_FILE to a file holding the admin password}
JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
step() { echo "== $*"; }

# call METHOD PATH [JSON] -> body on stdout; status in $STATUS_FILE
STATUS_FILE=$(mktemp)
trap 'rm -f "$JAR" "$STATUS_FILE"' EXIT
call() {
	method=$1 path=$2 body=${3:-}
	set -- -sS -b "$JAR" -c "$JAR" -X "$method" -H "Origin: $BASE" -H 'X-Requested-With: e2e' \
		-H "X-CSRF-Token: ${CSRF:-}" -o /dev/stdout -w '%{http_code}' "$BASE/api$path"
	if [ -n "$body" ]; then
		out=$(curl "$@" -H 'Content-Type: application/json' --data "$body")
	else
		out=$(curl "$@")
	fi
	code=$(printf '%s' "$out" | tail -c 3)
	printf '%s' "$code" > "$STATUS_FILE"
	printf '%s' "$out" | sed '$ s/...$//'
}
expect() {
	got=$(cat "$STATUS_FILE")
	[ "$got" = "$1" ] || fail "$2: HTTP $got, want $1"
}

step "unauthenticated request is refused"
call GET /users >/dev/null; expect 401 "no session"

step "sign in as $USER"
LOGIN=$(jq -n --arg l "$USER" --rawfile p "$PWFILE" '{login: $l, password: ($p | rtrimstr("\n"))}')
OUT=$(call POST /login "$LOGIN"); expect 200 login
CSRF=$(printf '%s' "$OUT" | jq -r .csrf_token)
[ "$(printf '%s' "$OUT" | jq -r .user.role)" = admin ] || fail "not admin"
grep -q boson_admin "$JAR" || fail "no session cookie"
grep -q 'at-\|bsr_' "$JAR" && fail "tokens visible in the cookie"

step "CSRF: state change without the token is refused"
SAVED=$CSRF; CSRF=wrong
call POST /logout >/dev/null; expect 403 "bad csrf"
CSRF=$SAVED

step "list users"
OUT=$(call GET '/users?limit=50'); expect 200 users
TOTAL=$(printf '%s' "$OUT" | jq .total)
echo "   $TOTAL users"
TARGET=$(printf '%s' "$OUT" | jq -r '[.users[] | select(.role=="user" and .disabled_at==null)][0].id')
[ "$TARGET" != null ] || fail "no regular user to disable"
NAME=$(printf '%s' "$OUT" | jq -r --arg id "$TARGET" '.users[] | select(.id==$id).username')

step "search users"
OUT=$(call GET "/users?q=$NAME"); expect 200 search
[ "$(printf '%s' "$OUT" | jq -r '.users[0].username')" = "$NAME" ] || fail "search did not find $NAME"

step "disable $NAME"
OUT=$(call POST "/users/$TARGET/disable"); expect 200 disable
[ "$(printf '%s' "$OUT" | jq -r .user.disabled_at)" != null ] || fail "not disabled"
step "enable $NAME"
OUT=$(call POST "/users/$TARGET/enable"); expect 200 enable
[ "$(printf '%s' "$OUT" | jq -r '.user.disabled_at // "none"')" = none ] || fail "still disabled"

step "user sessions"
call GET "/users/$TARGET/sessions" >/dev/null; expect 200 sessions

step "create invite"
OUT=$(call POST /invites '{"max_uses":2,"ttl":"168h","note":"e2e"}'); expect 201 "create invite"
INV=$(printf '%s' "$OUT" | jq -r .invite.id)
[ -n "$(printf '%s' "$OUT" | jq -r .invite.code)" ] || fail "no invite code"
OUT=$(call GET /invites); expect 200 invites
printf '%s' "$OUT" | jq -e --arg id "$INV" '.invites[] | select(.id==$id) | (.code // "") == ""' >/dev/null \
	|| fail "invite missing from the list, or its code is shown again"
step "revoke invite"
call DELETE "/invites/$INV" >/dev/null; expect 204 "revoke invite"
OUT=$(call GET /invites)
printf '%s' "$OUT" | jq -e --arg id "$INV" '.invites[] | select(.id==$id) | .revoked_at != null' >/dev/null || fail "invite not revoked"

step "audit log records the admin actions"
OUT=$(call GET '/audit?event=admin_*&since=1h'); expect 200 audit
for ev in admin_user_disabled admin_user_enabled admin_invite_created admin_invite_revoked; do
	printf '%s' "$OUT" | jq -e --arg e "$ev" 'any(.entries[]; .event==$e)' >/dev/null || fail "audit: no $ev"
done
printf '%s' "$OUT" | jq -e --arg n "$NAME" 'any(.entries[]; .target_username==$n)' >/dev/null || fail "audit: target not resolved"

step "stack overview"
OUT=$(call GET /overview); expect 200 overview
printf '%s' "$OUT" | jq -r '.services[] | "   \(.name): \(.status) \(.version // "")"'
printf '%s' "$OUT" | jq -r '.certificates[] | "   cert \(.target): \(.status)"'
printf '%s' "$OUT" | jq -r '"   metrics configured: \(.metrics.configured)"'

step "sign out"
call POST /logout >/dev/null; expect 204 logout
call GET /session >/dev/null; expect 401 "session after logout"

echo "OK"
