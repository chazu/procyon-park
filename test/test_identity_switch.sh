#!/usr/bin/env bash
# Integration test for `pp identity use` — builds a fresh pp binary and
# exercises identity switching under a scratch HOME.
set -euo pipefail

cd "$(dirname "$0")/.."
TMP=$(mktemp -d "/tmp/pp-switch-XXXXXX")
trap 'rm -rf "$TMP"' EXIT

rm -f pp-int && mag build -o pp-int >/dev/null && codesign -s - pp-int

PP="env HOME=$TMP ./pp-int"

$PP identity init alice >/dev/null
$PP identity init bob --force >/dev/null
# First-init wins: current should be alice (IdentityStore.init: only sets current when unset)
WHOAMI=$($PP whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "alice" ]] || { echo "FAIL: expected alice, got $WHOAMI"; exit 1; }

$PP identity use bob >/dev/null
WHOAMI=$($PP whoami | grep '^name:' | awk '{print $2}')
[[ "$WHOAMI" == "bob" ]] || { echo "FAIL: expected bob after use, got $WHOAMI"; exit 1; }

echo "PASS: identity switch"
