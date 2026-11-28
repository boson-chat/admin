package updates

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The helper jobs' shell scripts. Each runs in a short-lived docker:cli
// container with the Docker socket, the stack directory and the backup
// directory mounted at their host paths (so compose's relative paths and
// `docker run -v` sources resolve the same as on the host). Parameters come
// in as environment variables, never spliced into the script, and are
// validated first (validEnv). Results are lines the panel parses:
//
//	@@RESULT <kind> <json>    @@IMAGE <service> <ref> <image id> [digests…]
//	@@MIG <db> <table> <md5>  @@FILE <name> <bytes>   @@PRUNED <dir>
//	@@SET <KEY>=<value>       @@STEP <text>
//
// Common env: COMPOSE_PROJECT_NAME, COMPOSE_FILE (":"-joined absolute
// paths), STACK_DIR, ENV_FILE, BACKUP_ROOT, BACKUP_DIR, UTIL_IMAGE.

const prelude = `set -eu
dc() { docker compose --project-directory "$STACK_DIR" --env-file "$ENV_FILE" "$@"; }
step() { echo "@@STEP $*"; }
running() { [ -n "$(dc ps -q --status running "$1" 2>/dev/null)" ]; }
psql_q() { dc exec -T postgres sh -c 'psql -X -q -U "$POSTGRES_USER" -d "$1" -Atc "$2"' sh "$1" "$2"; }
util() { docker run --rm --label chat.boson.updates=util --network none "$@"; }
# setenv KEY=value...: rewrites ENV_FILE in place (same inode and mode), one
# line per key, appending missing keys.
setenv() {
	tmp="$ENV_FILE.boson-updates.tmp"
	cp -p "$ENV_FILE" "$tmp"
	[ -z "$(tail -c1 "$tmp")" ] || echo >> "$tmp"
	for kv in "$@"; do
		k=${kv%%=*}; v=${kv#*=}
		if grep -q "^$k=" "$tmp"; then
			awk -v k="$k" -v v="$v" 'index($0, k "=") == 1 { print k "=" v; next } { print }' "$tmp" > "$tmp.2"
			cat "$tmp.2" > "$tmp"; rm -f "$tmp.2"
		else
			printf '%s=%s\n' "$k" "$v" >> "$tmp"
		fi
		echo "@@SET $k=$v"
	done
	cat "$tmp" > "$ENV_FILE"; rm -f "$tmp"
}
# Migration fingerprint: every table that records applied migrations, per
# database, hashed. A changed hash after an update = migrations ran.
fingerprint() {
	running postgres || return 0
	for db in $(psql_q postgres "select datname from pg_database where not datistemplate and datname <> 'postgres' order by 1"); do
		for t in $(psql_q "$db" "select quote_ident(table_schema)||'.'||quote_ident(table_name) from information_schema.tables where table_type = 'BASE TABLE' and table_schema not in ('pg_catalog','information_schema') and table_name ~* '(migration|schema_version|goose_db_version)' order by 1"); do
			echo "@@MIG $db $t $(psql_q "$db" "select md5(coalesce(string_agg(x::text, ',' order by x::text), '')) from $t x")"
		done
	done
}
`

// scriptFiles: which of the COMPOSE_FILE candidates exist on the host
// (one "@@EXISTS <path>" line each). No compose here: a missing file is
// exactly what it is asked about.
const scriptFiles = `set -eu
echo "$COMPOSE_FILE" | tr ':' '\n' | while read -r f; do
	if [ -n "$f" ] && [ -f "$f" ]; then echo "@@EXISTS $f"; fi
done
echo "@@RESULT files {}"
`

// scriptPreflight: the compose project parses, and the backup fits.
// Env: VOLUMES (compose volume names without the project prefix).
const scriptPreflight = prelude + `
step "checking the compose project"
dc config -q
mkdir -p "$BACKUP_ROOT"
free=$(df -Pk "$BACKUP_ROOT" | awk 'NR == 2 { print $4 }' | tr -dc 0-9)
need=0
if running postgres; then
	need=$(psql_q postgres "select (coalesce(sum(pg_database_size(datname)), 0) / 1024)::bigint from pg_database" | tr -dc 0-9)
fi
for v in $VOLUMES; do
	full="${COMPOSE_PROJECT_NAME}_$v"
	docker volume inspect "$full" > /dev/null 2>&1 || continue
	k=$(util -v "$full:/v:ro" "$UTIL_IMAGE" du -sk /v | awk '{ print $1 }' | tr -dc 0-9)
	need=$((${need:-0} + ${k:-0}))
done
echo "@@RESULT preflight {\"free_kb\":${free:-0},\"need_kb\":${need:-0}}"
`

// scriptBackup: .env, config/, compose files, image digests, every stack
// database (pg_dump -Fc each, plus globals), data volumes, MinIO's metadata,
// the migration fingerprint; then prunes to the newest KEEP backups.
// Env: SERVICES (whose images are recorded), VOLUMES, KEEP.
const scriptBackup = prelude + `
umask 077
mkdir -p "$BACKUP_DIR"
touch "$BACKUP_DIR/.boson-update-backup"
step "saving .env and config"
cp -p "$ENV_FILE" "$BACKUP_DIR/env"
if [ -d "$STACK_DIR/config" ]; then tar czf "$BACKUP_DIR/config.tgz" -C "$STACK_DIR" config; fi
echo "$COMPOSE_FILE" | tr ':' '\n' | while read -r f; do
	if [ -n "$f" ] && [ -f "$f" ]; then cp -p "$f" "$BACKUP_DIR/compose-$(basename "$f")"; fi
done
if [ "$STACK_FILES" = 1 ]; then
	# A stack update: the whole stack directory (files, .boson state,
	# bridge/, secrets/), not the backups or the staged bundles.
	step "saving the stack directory"
	ex=""
	case "$BACKUP_ROOT/" in "$STACK_DIR"/*) ex="./${BACKUP_ROOT#"$STACK_DIR"/}" ;; esac
	tar czf "$BACKUP_DIR/stack-dir.tgz" -C "$STACK_DIR" --exclude=./.boson/staging --exclude=./backups ${ex:+--exclude="$ex"} .
fi
step "recording image digests"
: > "$BACKUP_DIR/images.txt"
for s in $SERVICES; do
	for id in $(dc ps -aq "$s" 2>/dev/null); do
		ref=$(docker inspect -f '{{.Config.Image}}' "$id")
		img=$(docker inspect -f '{{.Image}}' "$id")
		dig=$(docker image inspect -f '{{join .RepoDigests " "}}' "$img" 2>/dev/null || true)
		echo "@@IMAGE $s $ref $img $dig" | tee -a "$BACKUP_DIR/images.txt"
	done
done
if running postgres; then
	step "dumping the databases"
	dc exec -T postgres sh -c 'pg_dumpall -U "$POSTGRES_USER" --globals-only' > "$BACKUP_DIR/pg-globals.sql"
	for db in $(psql_q postgres "select datname from pg_database where not datistemplate and datname <> 'postgres' order by 1"); do
		dc exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -Fc -d "$1"' sh "$db" > "$BACKUP_DIR/pg-$db.dump"
	done
	fingerprint | tee "$BACKUP_DIR/migrations.txt"
fi
for v in $VOLUMES; do
	full="${COMPOSE_PROJECT_NAME}_$v"
	docker volume inspect "$full" > /dev/null 2>&1 || continue
	step "archiving volume $v"
	util -v "$full:/v:ro" -v "$BACKUP_DIR:/b" "$UTIL_IMAGE" tar czf "/b/vol-$v.tgz" -C /v .
done
full="${COMPOSE_PROJECT_NAME}_minio_data"
if docker volume inspect "$full" > /dev/null 2>&1; then
	step "archiving MinIO metadata (not the objects)"
	util -v "$full:/v:ro" -v "$BACKUP_DIR:/b" "$UTIL_IMAGE" sh -c 'cd /v && d=$(find . -maxdepth 3 -type d -name .minio.sys | head -n 1); [ -z "$d" ] || tar czf /b/minio-meta.tgz "$d"'
fi
for f in "$BACKUP_DIR"/*; do echo "@@FILE $(basename "$f") $(wc -c < "$f" | tr -d ' ')"; done
n=0
for d in $(ls -1d "$BACKUP_ROOT"/*/ 2>/dev/null | sort -r); do
	[ -f "$d/.boson-update-backup" ] || continue
	n=$((n + 1))
	[ "$n" -le "$KEEP" ] && continue
	[ "${d%/}" = "$BACKUP_DIR" ] && continue
	rm -rf "$d"
	echo "@@PRUNED $(basename "$d")"
done
echo "@@RESULT backup {\"dir\":\"$BACKUP_DIR\"}"
`

// scriptPull: the new images, before anything changes. Env: IMAGES.
const scriptPull = prelude + `
for i in $IMAGES; do step "pulling $i"; docker pull -q "$i"; done
echo "@@RESULT pull {}"
`

// scriptConfigure: the new versions into .env. Env: SETS (KEY=value…).
const scriptConfigure = prelude + `
setenv $SETS
echo "@@RESULT configure {}"
`

// scriptRecreate: one-off PRE services run to completion (migrations, a
// stack update's one-shot services), then SERVICES recreated without their
// dependencies (none: nothing is recreated), and FORCE recreated even
// though their compose configuration is the same (a file they bind-mount
// changed). LAST (the Docker socket proxies the panel works through) go
// at the very end: while compose replaces one, Docker's DNS has no name for
// it and the panel can't see the host, so nothing else changes after that.
const scriptRecreate = prelude + `
for s in $PRE; do step "running $s"; dc run --rm --no-deps "$s"; done
if [ -n "$SERVICES" ]; then
	step "recreating $SERVICES"
	dc up -d --no-deps $SERVICES
fi
if [ -n "$FORCE" ]; then
	# Their compose config is the same, but a file they read changed.
	step "restarting $FORCE (a file they read changed)"
	dc up -d --no-deps --force-recreate $FORCE
fi
if [ -n "$LAST" ]; then
	step "recreating $LAST (the panel's way to Docker: last)"
	dc up -d --no-deps $LAST
fi
echo "@@RESULT recreate {}"
`

// scriptFingerprint: the migration fingerprint now.
const scriptFingerprint = prelude + `
fingerprint
echo "@@RESULT fingerprint {}"
`

// scriptRollback: .env and config/ from the backup, the previous image
// digests pinned, then (RESTORE_DBS / RESTORE_VOLUMES) the data put back
// with its services (STOP) stopped, and SERVICES recreated.
//
// Postgres is never stopped here: the databases are restored through it.
// Before a database restore it is started (on the restored files) and
// waited for. If any step fails, the services this job stopped or was to
// recreate are still brought up on whatever files are in place by then
// (the restored ones once that step ran), so a failed rollback never
// leaves the stack stopped; "@@RECOVER ok|failed" says how that went.
const scriptRollback = prelude + `
has_service() { case " $(dc config --services 2>/dev/null | tr '\n' ' ') " in *" $1 "*) return 0 ;; esac; return 1; }
recover() {
	rc=$?
	trap - EXIT
	[ "$rc" -ne 0 ] || exit 0
	set +e
	up=""
	for s in $SERVICES $STOP; do
		if has_service "$s"; then up="$up $s"; fi
	done
	if has_service postgres; then up="postgres $up"; fi
	echo "@@STEP the rollback failed (exit $rc): starting$up again on the restored files"
	if out=$(dc up -d --no-deps $up 2>&1); then
		echo "@@RECOVER ok"
	else
		echo "$out" | sed 's/^/@@RECOVERLOG /'
		echo "@@RECOVER failed"
	fi
	exit "$rc"
}
trap recover EXIT
STOP=$(for s in $STOP; do [ "$s" = postgres ] || printf '%s ' "$s"; done)
if [ "$STACK_RESTORE" = 1 ] && [ -f "$BACKUP_DIR/stack-dir.tgz" ]; then
	step "restoring the stack files"
	if [ -f "$BACKUP_DIR/stack-added.txt" ]; then
		while read -r p; do
			case "$p" in ''|/*|*..*) continue ;; esac
			rm -f "$STACK_DIR/$p"
		done < "$BACKUP_DIR/stack-added.txt"
	fi
	# The stack's own records (.boson/) go back to the backup's, including
	# ones the update created: tar puts back what was there, never removes
	# what wasn't. A migrations.applied left from the update (the first
	# stack update creates it) would say its migrations ran on the .env
	# put back from before them, and the next update would skip them.
	for f in migrations.applied stack-files.sha256 manifest.json; do
		rm -f "$STACK_DIR/.boson/$f"
	done
	tar xzf "$BACKUP_DIR/stack-dir.tgz" -C "$STACK_DIR"
	# Services the update added are gone from the compose files again.
	have=" $(dc config --services 2>/dev/null | tr '\n' ' ') "
	keep=""
	for s in $SERVICES; do
		case "$have" in
		*" $s "*) keep="$keep $s" ;;
		*)
			echo "@@REMOVED $s"
			docker ps -aq --filter "label=com.docker.compose.project=$COMPOSE_PROJECT_NAME" --filter "label=com.docker.compose.service=$s" | xargs -r docker rm -f > /dev/null
			;;
		esac
	done
	SERVICES=$keep
	# Nothing to stop of what the restored files don't have (compose would
	# refuse the whole stop); it is left as it is.
	keep=""
	for s in $STOP; do
		case "$have" in
		*" $s "*) keep="$keep $s" ;;
		*) echo "@@NOTSTOPPED $s" ;;
		esac
	done
	STOP=$keep
fi
step "restoring .env and config"
cat "$BACKUP_DIR/env" > "$ENV_FILE"
if [ -f "$BACKUP_DIR/config.tgz" ]; then tar xzf "$BACKUP_DIR/config.tgz" -C "$STACK_DIR"; fi
[ -z "$SETS" ] || setenv $SETS
if [ -n "$(echo $STOP)" ]; then step "stopping $(echo $STOP)"; dc stop $STOP; fi
if [ -n "$RESTORE_DBS" ]; then
	step "starting postgres on the restored files"
	dc up -d --no-deps --wait --wait-timeout 120 postgres
fi
for db in $RESTORE_DBS; do
	step "restoring database $db"
	owner=$(psql_q postgres "select pg_get_userbyid(datdba) from pg_database where datname = '$db'")
	dc exec -T postgres sh -c 'dropdb -U "$POSTGRES_USER" --if-exists --force "$1" && createdb -U "$POSTGRES_USER" -O "$2" "$1"' sh "$db" "${owner:-$db}"
	dc exec -T postgres sh -c 'pg_restore -U "$POSTGRES_USER" --exit-on-error -1 -d "$1"' sh "$db" < "$BACKUP_DIR/pg-$db.dump"
done
for v in $RESTORE_VOLUMES; do
	[ -f "$BACKUP_DIR/vol-$v.tgz" ] || continue
	step "restoring volume $v"
	util -v "${COMPOSE_PROJECT_NAME}_$v:/v" -v "$BACKUP_DIR:/b:ro" "$UTIL_IMAGE" sh -c 'find /v -mindepth 1 -delete && tar xzf "/b/vol-$1.tgz" -C /v' sh "$v"
done
up=$(echo $SERVICES $STOP)
if [ -n "$up" ]; then step "recreating $up"; dc up -d --no-deps $up; fi
trap - EXIT
echo "@@RESULT rollback {}"
`

// scriptStackState: the installed stack release (VERSION), whether its
// files are recorded, the migrations it ran, .env's *_IMAGE pins and
// COMPOSE_PROFILES. Nothing else of .env is printed (it has secrets).
const scriptStackState = `set -eu
v=""
[ ! -f "$STACK_DIR/VERSION" ] || v=$(head -n 1 "$STACK_DIR/VERSION" | tr -dc '0-9A-Za-z.+-')
echo "@@STACKVERSION ${v:-none}"
if [ -f "$STACK_DIR/.boson/stack-files.sha256" ]; then echo "@@BASELINE yes"; else echo "@@BASELINE no"; fi
[ ! -f "$STACK_DIR/.boson/migrations.applied" ] || awk 'NF { print "@@APPLIED " $1 }' "$STACK_DIR/.boson/migrations.applied"
grep -E '^[A-Z][A-Z0-9_]*_IMAGE=' "$ENV_FILE" | tr -d '\r' | sed 's/^/@@ENVIMG /' || true
p=$(grep -E '^COMPOSE_PROFILES=' "$ENV_FILE" | tail -n 1 | cut -d= -f2- | tr -dc 'A-Za-z0-9,_-')
echo "@@PROFILES $p"
echo "@@RESULT stack-state {}"
`

// scriptStage: the release bundle into STACK_DIR/.boson/staging/
// STACK_VERSION: downloaded from BUNDLE_URL unless a copy with the right
// checksum is there already, checked against BUNDLE_SHA256, unpacked.
// The only job with a network (to GitHub). Older staged releases go.
const scriptStage = `set -eu
step() { echo "@@STEP $*"; }
d="$STACK_DIR/.boson/staging"
tgz="$d/boson-stack-$STACK_VERSION.tar.gz"
out="$d/$STACK_VERSION"
mkdir -p "$d"
chmod 700 "$d"
good() { [ -f "$tgz" ] && echo "$BUNDLE_SHA256  $tgz" | sha256sum -c - > /dev/null 2>&1; }
if ! good; then
	step "downloading the stack release $STACK_VERSION"
	rm -f "$tgz" "$tgz.part"
	if command -v wget > /dev/null 2>&1; then wget -q -O "$tgz.part" "$BUNDLE_URL"; else curl -fsSL -o "$tgz.part" "$BUNDLE_URL"; fi
	mv -f "$tgz.part" "$tgz"
	if ! good; then
		rm -f "$tgz"
		echo "the downloaded bundle doesn't match its SHA-256 ($BUNDLE_SHA256)"
		exit 1
	fi
fi
step "unpacking (SHA-256 $BUNDLE_SHA256 checked)"
rm -rf "$out"
mkdir -p "$out"
tar xzf "$tgz" -C "$out"
for f in .bundle/manifest.json .bundle/files.sha256 scripts/stack-apply.sh VERSION; do
	[ -f "$out/$f" ] || { echo "the bundle has no $f"; exit 1; }
done
for x in "$d"/*; do
	case "$x" in "$tgz" | "$out") ;; *) rm -rf "$x" ;; esac
done
echo "@@RESULT stage {\"dir\":\"$out\"}"
`

// hashes prints compose's config hash of every service of the active
// profiles: "@@HASH <label> <service> <hash>". STAGED=1 hashes the staged
// release's compose files in place of the installed ones (paths stay the
// install's: --project-directory).
const stackHashes = `
hashes() {
	files=$COMPOSE_FILE
	if [ "${2:-}" = staged ]; then
		files=""
		for f in $(echo "$COMPOSE_FILE" | tr ':' ' '); do
			rel=${f#"$STACK_DIR"/}
			if [ "$rel" != "$f" ] && [ -f "$STAGE_DIR/$rel" ]; then f="$STAGE_DIR/$rel"; fi
			files="${files:+$files:}$f"
		done
	fi
	COMPOSE_FILE=$files dc config --hash '*' | awk -v l="$1" 'NF == 2 { print "@@HASH " l " " $1 " " $2 }'
}
`

// scriptStackPlan: what the staged release would change, without changing
// anything: the bundle's stack-apply.sh plan (files, diffs, migrations) and
// the config hashes now and with the release's compose files.
const scriptStackPlan = prelude + stackHashes + `
sh "$STAGE_DIR/scripts/stack-apply.sh" plan --diff "$STAGE_DIR"
hashes old
hashes new staged
echo "@@RESULT stack-plan {}"
`

// scriptStackApply: the staged release in place. Config hashes before; the
// bundle's files; SETS (image versions) into .env; the migrations; config
// hashes after. The services to recreate are the panel's to work out.
const scriptStackApply = prelude + stackHashes + `
hashes old
step "writing the stack files of $STACK_VERSION"
sh "$STAGE_DIR/scripts/stack-apply.sh" files "$STAGE_DIR"
[ -z "$SETS" ] || setenv $SETS
step "running the stack migrations"
sh "$STAGE_DIR/scripts/stack-apply.sh" migrate "$STAGE_DIR"
hashes new
echo "@@RESULT stack-apply {}"
`

// scriptSelf: the panel's own update, run detached so it outlives the
// panel. TARGET_SETS (ADMIN_IMAGE=…) into .env, recreate admin, and wait
// up to SELF_WAIT×2 s for a container of that image to stay up for 20 s; if
// it doesn't, FALLBACK_SETS (the version that ran) go back and admin is
// recreated again.
const scriptSelf = prelude + `
sleep 3
want=${TARGET_SETS#*=}
setenv $TARGET_SETS
step "recreating admin"
dc up -d --no-deps admin || echo "compose up failed"
ok=0; stable=0; i=0
while [ "$i" -lt "${SELF_WAIT:-90}" ]; do
	id=$(dc ps -q admin 2>/dev/null | head -n 1)
	st=""
	[ -z "$id" ] || st=$(docker inspect -f '{{.Config.Image}} {{.State.Status}} {{.RestartCount}} {{if .State.Health}}{{.State.Health.Status}}{{end}}' "$id" 2>/dev/null || true)
	case "$st" in
	"$want running 0 "|"$want running 0 healthy"|"$want running 0 starting") stable=$((stable + 1)) ;;
	*) stable=0 ;;
	esac
	if [ "$stable" -ge 10 ]; then ok=1; break; fi
	sleep 2; i=$((i + 1))
done
if [ "$ok" = 1 ]; then echo "@@RESULT self {\"ok\":true}"; exit 0; fi
step "the new panel did not stay up: putting the previous one back"
setenv $FALLBACK_SETS
dc up -d --no-deps admin
echo "@@RESULT self {\"ok\":false,\"fell_back\":true}"
`

var (
	reService = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	reEnvKey  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	reValue   = regexp.MustCompile(`^[A-Za-z0-9./:@_+-]+$`)
	rePath    = regexp.MustCompile(`^/[A-Za-z0-9./_ +@-]*$`)
)

// validEnv refuses anything a helper script could misread: names and image
// references are plain words, paths are absolute without quotes or newlines.
func validEnv(env map[string]string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := env[k]
		switch k {
		case "STACK_DIR", "ENV_FILE", "BACKUP_ROOT", "BACKUP_DIR", "STAGE_DIR":
			if !rePath.MatchString(v) || strings.Contains(v, "..") {
				return fmt.Errorf("%s: not a plain absolute path: %q", k, v)
			}
		case "COMPOSE_FILE":
			for _, p := range strings.Split(v, ":") {
				if !rePath.MatchString(p) || strings.Contains(p, "..") {
					return fmt.Errorf("COMPOSE_FILE: not a plain absolute path: %q", p)
				}
			}
		case "SERVICES", "PRE", "STOP", "VOLUMES", "RESTORE_DBS", "RESTORE_VOLUMES", "FORCE", "LAST":
			for _, w := range strings.Fields(v) {
				if !reService.MatchString(w) {
					return fmt.Errorf("%s: bad name %q", k, w)
				}
			}
		case "SETS", "TARGET_SETS", "FALLBACK_SETS":
			for _, kv := range strings.Fields(v) {
				kk, vv, ok := strings.Cut(kv, "=")
				if !ok || !reEnvKey.MatchString(kk) || !reValue.MatchString(vv) {
					return fmt.Errorf("%s: bad assignment %q", k, kv)
				}
			}
		case "IMAGES", "UTIL_IMAGE":
			for _, w := range strings.Fields(v) {
				if !reValue.MatchString(w) {
					return fmt.Errorf("%s: bad image %q", k, w)
				}
			}
		case "KEEP", "SELF_WAIT":
			if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(v) {
				return fmt.Errorf("%s: %q", k, v)
			}
		case "STACK_FILES", "STACK_RESTORE":
			if v != "" && v != "1" {
				return fmt.Errorf("%s: %q", k, v)
			}
		case "STACK_VERSION":
			if _, ok := ParseVersion(v); !ok || !reValue.MatchString(v) {
				return fmt.Errorf("STACK_VERSION: %q", v)
			}
		case "BUNDLE_URL":
			if !reURL.MatchString(v) {
				return fmt.Errorf("BUNDLE_URL: %q", v)
			}
		case "BUNDLE_SHA256":
			if !reSHA256.MatchString(v) {
				return fmt.Errorf("BUNDLE_SHA256: %q", v)
			}
		case "COMPOSE_PROJECT_NAME":
			if !reService.MatchString(v) {
				return fmt.Errorf("COMPOSE_PROJECT_NAME: %q", v)
			}
		default:
			return fmt.Errorf("unknown helper variable %s", k)
		}
	}
	return nil
}
