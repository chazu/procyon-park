#!/usr/bin/env bash
# Run all shell-based integration tests for the multiplayer UX.
# Each individual test builds its own pp binary under a scratch HOME.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "=== identity switch + override ==="
./test/test_identity_switch.sh

echo "=== auto bootstrap ==="
./test/test_auto_bootstrap.sh

echo "=== invite end-to-end ==="
./test/test_invite_e2e.sh

echo ""
echo "All integration tests passed."
