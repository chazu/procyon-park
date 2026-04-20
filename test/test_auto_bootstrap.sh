#!/usr/bin/env bash
# Integration test: first-run `pp serve` in an empty $HOME auto-bootstraps
# a 'local' identity, writes server.toml, and subsequent signed CLI calls
# succeed without manual admin registration.
#
# Port: uses --port flag (supported since Main.mag flags parsing).
# Isolation: a scratch $HOME dir isolates both ~/.config/pp and ~/.pp data.
set -euo pipefail

cd "$(dirname "$0")/.."
TMP=$(mktemp -d "/tmp/pp-boot-XXXXXX")
PORT=$((7000 + RANDOM % 500))
SERVER=""
LOG=/tmp/pp-boot.log

cleanup() {
  [[ -n "$SERVER" ]] && kill "$SERVER" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

echo "Building pp-int..."
rm -f pp-int
mag build -o pp-int >/dev/null
codesign -s - pp-int

echo "Starting server on port $PORT with scratch HOME=$TMP ..."
env HOME="$TMP" ./pp-int serve --port "$PORT" >"$LOG" 2>&1 &
SERVER=$!

# Wait for server to be ready (up to 10s)
for i in $(seq 1 10); do
  sleep 1
  curl -s "http://localhost:$PORT/api/health" >/dev/null 2>&1 && break
  if ! kill -0 "$SERVER" 2>/dev/null; then
    echo "FAIL: server exited early"
    cat "$LOG"
    exit 1
  fi
done

# Verify server is still running
if ! kill -0 "$SERVER" 2>/dev/null; then
  echo "FAIL: server not running after startup wait"
  cat "$LOG"
  exit 1
fi

# --- File assertions ---

[[ -f "$TMP/.config/pp/server.toml" ]] || {
  echo "FAIL: server.toml not written"
  cat "$LOG"
  exit 1
}

[[ -f "$TMP/.config/pp/identity/local.key" ]] || {
  echo "FAIL: local.key not generated"
  cat "$LOG"
  exit 1
}

grep -q 'admins' "$TMP/.config/pp/server.toml" || {
  echo "FAIL: admins key missing from server.toml"
  cat "$TMP/.config/pp/server.toml"
  exit 1
}

grep -q 'enforce_signatures' "$TMP/.config/pp/server.toml" || {
  echo "FAIL: enforce_signatures key missing from server.toml"
  cat "$TMP/.config/pp/server.toml"
  exit 1
}

echo "File assertions passed."

# --- whoami: should resolve 'local' as active identity ---

WHOAMI=$(env HOME="$TMP" PP_URL="http://localhost:$PORT" ./pp-int whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "local" ]] || {
  echo "FAIL: whoami returned '$WHOAMI', expected 'local'"
  cat "$LOG"
  exit 1
}

echo "whoami passed: $WHOAMI"

# --- Signed end-to-end: pp observe should not be rejected ---

OUT=$(env HOME="$TMP" PP_URL="http://localhost:$PORT" ./pp-int observe local "bootstrap smoke test" 2>&1 || true)
echo "$OUT" | grep -q '"error"' && {
  echo "FAIL: signed observe rejected: $OUT"
  cat "$LOG"
  exit 1
}

echo "observe passed."
echo ""
echo "PASS: auto-bootstrap"
