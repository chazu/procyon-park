#!/usr/bin/env bash
# End-to-end test: admin auto-bootstraps, creates an invite, invitee
# accepts it (in a separate HOME), and immediately makes a signed
# observation that the server accepts.
set -euo pipefail

cd "$(dirname "$0")/.."
ADMIN_HOME=$(mktemp -d "/tmp/pp-inv-admin-XXXXXX")
INVITEE_HOME=$(mktemp -d "/tmp/pp-inv-user-XXXXXX")
PORT=$((7500 + RANDOM % 300))
SERVER=""
cleanup() {
  [[ -n "$SERVER" ]] && kill "$SERVER" 2>/dev/null || true
  rm -rf "$ADMIN_HOME" "$INVITEE_HOME"
}
trap cleanup EXIT

rm -f pp-int && mag build -o pp-int >/dev/null && codesign -s - pp-int

# Start server under ADMIN_HOME — auto-bootstraps 'local' as admin.
env HOME=$ADMIN_HOME ./pp-int serve --port $PORT >/tmp/pp-inv.log 2>&1 &
SERVER=$!
sleep 4

# Admin creates an invite for 'alice'.
INVITE_OUT=$(env HOME=$ADMIN_HOME PP_URL=http://localhost:$PORT ./pp-int identity invite alice --ttl 600 2>&1 || true)
echo "$INVITE_OUT"
TOKEN=$(echo "$INVITE_OUT" | grep -oE '"token":[[:space:]]*"[^"]+"' | head -1 | sed -E 's/.*"([^"]+)"$/\1/')
[[ -n "$TOKEN" ]] || { echo "FAIL: no token in invite output"; cat /tmp/pp-inv.log; exit 1; }

# Invitee (separate HOME, no existing identity) accepts.
env HOME=$INVITEE_HOME ./pp-int identity accept http://localhost:$PORT --name alice --token "$TOKEN" || { echo "FAIL: accept failed"; exit 1; }

# Invitee is now 'alice' — whoami confirms.
WHOAMI=$(env HOME=$INVITEE_HOME ./pp-int whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "alice" ]] || { echo "FAIL: whoami after accept returned $WHOAMI"; exit 1; }

# Invitee makes a signed observation — server must accept.
OUT=$(env HOME=$INVITEE_HOME PP_URL=http://localhost:$PORT ./pp-int observe alice "invite flow works" 2>&1 || true)
echo "$OUT" | grep -q '"error"' && { echo "FAIL: signed observe rejected after accept: $OUT"; cat /tmp/pp-inv.log; exit 1; }

echo "PASS: invite end-to-end"
