#!/bin/sh
# End-to-end test of herdr-share in an isolated environment.
#
# Everything runs in a throwaway folder: its own herdr-core session (HC_SESSION,
# XDG_STATE_HOME, XDG_CONFIG_HOME), its own herdr-share home, its own Herdr app
# home and a relay on a free local port. Your own terminals, Herdr settings and
# herdr-share config are never touched.
#
# Usage: scripts/e2e.sh [--relay binary|docker] [--keep]
#   --relay docker   run the relay from the image (built from ./Dockerfile
#                    unless E2E_IMAGE names one)
#   --keep           leave the work folder for inspection
#
# Needs: hc (herdr-core), node, and bin/herdr-share + bin/herdr-share-relay
# (`make build`, with the web client built by `make web`). herdr-app is used
# to check the mod when it is installed. PW_CHANNEL=chrome uses the installed
# Chrome instead of Playwright's Chromium.
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
RELAY_MODE=binary
KEEP=0
while [ $# -gt 0 ]; do
	case $1 in
	--relay) RELAY_MODE=$2; shift 2 ;;
	--keep) KEEP=1; shift ;;
	*) echo "unknown argument $1" >&2; exit 2 ;;
	esac
done

SHARE="$ROOT/bin/herdr-share"
RELAY_BIN="$ROOT/bin/herdr-share-relay"
[ -x "$SHARE" ] && [ -x "$RELAY_BIN" ] || { echo "build first: make web build" >&2; exit 1; }
command -v hc >/dev/null || { echo "hc (herdr-core) is required" >&2; exit 1; }

# A short path: Unix socket paths are limited to ~100 bytes.
WORK=$(mktemp -d /tmp/hse2e.XXXXXX)
export HC_SESSION="hse2e-$$"
export XDG_STATE_HOME="$WORK/state" XDG_CONFIG_HOME="$WORK/config"
export HERDR_SHARE_HOME="$WORK/share" HERDR_APP_HOME="$WORK/app"
export HERDR_RELEASES_URL="file:///nonexistent" DO_NOT_TRACK=1
unset HC_TERMINAL_ID HC_TERMINAL_SOCKET HC_SOCKET 2>/dev/null || true
mkdir -p "$XDG_STATE_HOME" "$XDG_CONFIG_HOME" "$HERDR_APP_HOME" "$WORK/artifacts"

RELAY_PID=""
CONTAINER=""
cleanup() {
	status=$?
	"$SHARE" stop --all >/dev/null 2>&1 || true
	hc server stop >/dev/null 2>&1 || true
	[ -n "$RELAY_PID" ] && kill "$RELAY_PID" 2>/dev/null || true
	[ -n "$CONTAINER" ] && docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
	if [ $status -ne 0 ]; then
		echo "--- relay log"; tail -n 40 "$WORK/relay.log" 2>/dev/null || true
		for f in "$HERDR_SHARE_HOME"/state/logs/*.log; do [ -f "$f" ] && { echo "--- $f"; tail -n 40 "$f"; }; done
		echo "e2e FAILED (work folder: $WORK)"
	fi
	if [ $KEEP -eq 0 ] && [ $status -eq 0 ]; then rm -rf "$WORK"; fi
	exit $status
}
trap cleanup EXIT INT TERM

say() { printf '\n== %s\n' "$*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
json() { node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const v=JSON.parse(s);const r=(new Function("v","return "+process.argv[1]))(v);console.log(typeof r==="string"?r:JSON.stringify(r))})' "$1"; }

PORT=$(node -e 'const s=require("net").createServer().listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')
RELAY="http://127.0.0.1:$PORT"
TOKEN="e2e-$(node -e 'console.log(require("crypto").randomBytes(8).toString("hex"))')"

say "relay ($RELAY_MODE) on $RELAY"
if [ "$RELAY_MODE" = docker ]; then
	IMAGE=${E2E_IMAGE:-herdr-share-relay:e2e}
	if [ -z "${E2E_IMAGE:-}" ]; then docker build -q -t "$IMAGE" "$ROOT" >/dev/null; fi
	CONTAINER="hse2e-relay-$$"
	docker run -d --name "$CONTAINER" -p "127.0.0.1:$PORT:8080" -e HERDR_SHARE_TOKENS="$TOKEN" "$IMAGE" >/dev/null
	for _ in $(seq 1 30); do
		[ "$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER")" = healthy ] && break
		sleep 1
	done
	[ "$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER")" = healthy ] || fail "relay container is not healthy"
else
	"$RELAY_BIN" -listen "127.0.0.1:$PORT" -tokens "$TOKEN" >"$WORK/relay.log" 2>&1 &
	RELAY_PID=$!
	for _ in $(seq 1 50); do curl -fsS "$RELAY/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
fi
curl -fsS "$RELAY/api/v1/info" | json 'v.auth_required' | grep -qx true || fail "relay does not require a token"

say "isolated herdr-core ($HC_SESSION)"
hc server start >/dev/null
# Sized like a Herdr pane (hc run has no size flag; the API does).
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"terminal.run","params":{"argv":["sh","-c","echo READY-$((6*7)); while IFS= read -r l; do echo \"got:$l\"; done"],"name":"e2e-target","size":{"cols":110,"rows":32},"no_parent":true,"no_notify":true}}' |
	hc connect | grep -q '"result"' || fail "could not start the target terminal"
hc run --bg --name e2e-other --no-parent -- sh -c 'echo OTHER; exec cat' >/dev/null

say "configure herdr-share"
"$SHARE" config set relay "$RELAY" >/dev/null
"$SHARE" start e2e-target --json >/dev/null 2>&1 && fail "sharing without the relay token should fail"
"$SHARE" config set token "$TOKEN" >/dev/null
"$SHARE" config show --json | json 'v.has_token' | grep -qx true || fail "token not saved"
"$SHARE" config show --json | grep -q "$TOKEN" && fail "config show leaks the token"

say "Herdr mod"
"$SHARE" mod install --no-bind >/dev/null
[ -f "$HERDR_APP_HOME/mods/herdr-share/main.js" ] || fail "mod not installed"
json 'v.bin' <"$HERDR_APP_HOME/mods/herdr-share/install.json" | grep -q "herdr-share$" || fail "install.json has no binary"
if command -v herdr-app >/dev/null; then
	herdr-app check "$HERDR_APP_HOME/mods/herdr-share" || fail "herdr-app check failed"
fi
"$SHARE" doctor || fail "doctor reports a problem"

say "start sharing (view only)"
OUT=$("$SHARE" start e2e-target --mode view --ttl 30m --label "e2e · browser" --json)
LINK=$(echo "$OUT" | json 'v.link')
case $LINK in "$RELAY"/s/*#*) ;; *) fail "unexpected link $LINK" ;; esac
echo "$OUT" | json 'v.mode' | grep -qx view || fail "mode is not view"
AGAIN=$("$SHARE" start e2e-target --json)
[ "$(echo "$AGAIN" | json 'v.existing')" = true ] || fail "sharing twice should return the existing share"
[ "$(echo "$AGAIN" | json 'v.link')" = "$LINK" ] || fail "existing share has another link"
[ "$("$SHARE" list --json | json 'v.length')" = 1 ] || fail "list should show one share"
hc ls --json | json 'v.terminals.filter(t=>t.name&&t.name.startsWith("herdr-share-")).length' | grep -qx 1 || fail "bridge is not running as an hc terminal"
"$SHARE" status e2e-target >/dev/null
"$SHARE" extend e2e-target 1h --json | json 'v.expires_at' >/dev/null

say "browser guest"
(
	cd "$ROOT/test/browser"
	[ -d node_modules ] || npx -y "$(node -p "require('./package.json').packageManager")" install --frozen-lockfile >/dev/null
	E2E_LINK="$LINK" E2E_RELAY="$RELAY" E2E_TERMINAL=e2e-target E2E_SHARE="$SHARE" E2E_ARTIFACTS="$WORK/artifacts" node guest.mjs
)
[ "$("$SHARE" list --json | json 'v.length')" = 0 ] || fail "the share is still listed after stopping"
[ "$(hc get --json e2e-target | json 'v.state')" = live ] || fail "stopping the share ended the terminal"

say "the share ends with its terminal"
"$SHARE" start e2e-other --mode control --ttl 0 --json >/dev/null
hc kill e2e-other >/dev/null
for _ in $(seq 1 50); do
	[ "$("$SHARE" list --json | json 'v.length')" = 0 ] && break
	sleep 0.2
done
[ "$("$SHARE" list --json | json 'v.length')" = 0 ] || fail "share outlived its terminal"
hc ls --json | json 'v.terminals.filter(t=>t.name&&t.name.startsWith("herdr-share-")).length' | grep -qx 0 || fail "a bridge terminal was left behind"

say "stop --all and mod uninstall"
"$SHARE" start e2e-target --json >/dev/null
"$SHARE" stop --all >/dev/null
[ "$("$SHARE" list --json | json 'v.length')" = 0 ] || fail "stop --all left shares"
"$SHARE" mod uninstall >/dev/null
[ ! -e "$HERDR_APP_HOME/mods/herdr-share" ] || fail "mod not removed"

say "e2e passed"
