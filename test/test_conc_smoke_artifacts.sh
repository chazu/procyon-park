#!/usr/bin/env bash
#
# Spec test: Concurrency smoke artifacts (mission msn-b5e3103ead28984e4f66f2e8a14f38ed)
#
# Verifies the three trivial markdown artifacts under experiments/conc-smoke/
# match the story specs EXACTLY:
#
#   alpha.md    -> line1 "# Alpha"    line2 "Concurrency smoke story A."
#   bravo.md    -> line1 "# Bravo"    line2 "Concurrency smoke story B."
#   charlie.md  -> line1 "# Charlie"  line2 "Concurrency smoke story C; follows Alpha."
#
# Each file must have EXACTLY two content lines (spec: "exactly two lines").
# This test is written from the SPEC, not the implementation.
#
# Source resolution:
#   - Default: reads files from the working tree (post-merge on main).
#   - If CONC_SMOKE_REF is set, reads files from that git ref via `git show`
#     (e.g. CONC_SMOKE_REF=feature/full-pipeline-... to test pre-merge).
#
# Exit 0 iff every assertion passes.

set -u

DIR="experiments/conc-smoke"
REF="${CONC_SMOKE_REF:-}"

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo "FAIL: not inside a git repo"; exit 2; }
cd "$ROOT" || exit 2

pass=0
fail=0

# read_file <relpath> -> stdout (empty + rc!=0 if missing)
read_file() {
  local rel="$1"
  if [ -n "$REF" ]; then
    git show "$REF:$rel" 2>/dev/null
  else
    cat "$rel" 2>/dev/null
  fi
}

exists_file() {
  local rel="$1"
  if [ -n "$REF" ]; then
    git cat-file -e "$REF:$rel" 2>/dev/null
  else
    [ -f "$rel" ]
  fi
}

check() {
  # check <name> <line1> <line2>
  local name="$1" want1="$2" want2="$3"
  local rel="$DIR/$name"
  local content nlines got1 got2

  if ! exists_file "$rel"; then
    echo "FAIL [$name]: file does not exist at $rel"; fail=$((fail+1)); return
  fi

  content="$(read_file "$rel")"

  # Exactly two content lines. `grep -c ''` counts lines (trailing newline
  # produces 2 for a well-formed 2-line file).
  nlines="$(read_file "$rel" | grep -c '')"
  if [ "$nlines" -ne 2 ]; then
    echo "FAIL [$name]: expected exactly 2 lines, got $nlines"; fail=$((fail+1)); return
  fi

  got1="$(printf '%s\n' "$content" | sed -n '1p')"
  got2="$(printf '%s\n' "$content" | sed -n '2p')"

  if [ "$got1" != "$want1" ]; then
    echo "FAIL [$name]: line1 mismatch"
    echo "        want: '$want1'"
    echo "        got:  '$got1'"
    fail=$((fail+1)); return
  fi
  if [ "$got2" != "$want2" ]; then
    echo "FAIL [$name]: line2 mismatch"
    echo "        want: '$want2'"
    echo "        got:  '$got2'"
    fail=$((fail+1)); return
  fi

  echo "PASS [$name]: heading + body match spec (2 lines)"
  pass=$((pass+1))
}

echo "== conc-smoke artifact spec test (ref='${REF:-working-tree}') =="
check "alpha.md"   "# Alpha"   "Concurrency smoke story A."
check "bravo.md"   "# Bravo"   "Concurrency smoke story B."
check "charlie.md" "# Charlie" "Concurrency smoke story C; follows Alpha."

echo "---"
echo "$pass passing, $fail failures"
[ "$fail" -eq 0 ]
