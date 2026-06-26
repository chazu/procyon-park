#!/usr/bin/env bash
# End-to-end / dispatch-level test for the `pp doctrine` CLI.
#
# WHY THIS EXISTS (root cause of a shipped bug): the crystallization epic
# shipped a defect where two wave-mate stories each defined
# DoctrineCLI>>cmdCrystallize: (--as workitem vs --as convention). The git
# merge was CLEAN but in Maggie last-definition-wins silently SHADOWED the
# workitem handler, so `crystallize --as workitem` was rejected end-to-end.
# The helper unit tests (test/bbs/test_doctrine_crystallize.mag) never caught
# it because they exercise helper methods directly, NEVER the runWith:
# dispatch. Only a live smoke test found it (fixed in commit 33db668).
#
# This test drives the REAL `pp doctrine` command dispatch against a live
# server so a shadowed / missing / misrouted subcommand fails CI instead of
# shipping. The critical assertion is that BOTH `crystallize --as workitem`
# AND `crystallize --as convention` reach their OWN logic.
#
# SCOPE GOTCHA: `pp doctrine` operates at the tuple STORAGE scope (PP_SCOPE),
# not the payload doctrine_scope from --applies. We pin PP_SCOPE for
# determinism and read tuples back at that same scope.
set -euo pipefail

cd "$(dirname "$0")/.."
TMP=$(mktemp -d "/tmp/pp-doctrine-XXXXXX")
PORT=$((7800 + RANDOM % 300))
SCOPE="doctrine-e2e"
SERVER=""
LOG=/tmp/pp-doctrine.log
trap 'cleanup' EXIT

cleanup() {
  [[ -n "$SERVER" ]] && kill "$SERVER" 2>/dev/null || true
  rm -rf "$TMP"
}

# Run a `pp doctrine ...` (or any pp) command under the scratch HOME + scope.
pp() {
  env HOME="$TMP" PP_URL="http://localhost:$PORT" PP_SCOPE="$SCOPE" ./pp-int "$@"
}

fail() {
  echo "FAIL: $1"
  echo "----- server log -----"
  cat "$LOG" 2>/dev/null || true
  exit 1
}

# Assert that $1 (haystack) contains $2 (needle); $3 = description.
assert_contains() {
  case "$1" in
    *"$2"*) : ;;
    *) echo "----- output -----"; echo "$1"; fail "$3 (expected to contain: $2)" ;;
  esac
}

# Assert that $1 (haystack) does NOT contain $2 (needle); $3 = description.
assert_not_contains() {
  case "$1" in
    *"$2"*) echo "----- output -----"; echo "$1"; fail "$3 (did NOT expect: $2)" ;;
    *) : ;;
  esac
}

# ---------------------------------------------------------------------------
# BONUS guard (structural): no DoctrineCLI selector is defined twice.
# This catches the exact duplicate-selector shadowing bug class at the source
# level, independent of the live e2e below. A keyword selector is the
# concatenation of every ':'-terminated token in the `method:` declaration
# (param names do not end in ':'); a unary selector is the single token.
# ---------------------------------------------------------------------------
echo "=== structural guard: no duplicate DoctrineCLI selectors ==="
DUP=$(awk '
  /^[[:space:]]*method:[[:space:]]/ {
    sel = ""; kw = 0;
    for (i = 2; i <= NF; i++) {
      tok = $i;
      if (tok == "[") break;
      if (tok ~ /:$/) { sel = sel tok; kw = 1; }
      else if (kw == 0 && sel == "") { sel = tok; }
    }
    if (sel != "") print sel;
  }
' src/cli/DoctrineCLI.mag | sort | uniq -d)
if [[ -n "$DUP" ]]; then
  echo "Duplicate selector(s) defined in DoctrineCLI.mag:"
  echo "$DUP"
  fail "duplicate DoctrineCLI selector(s) — last-definition-wins will shadow a handler"
fi
echo "structural guard passed."

# ---------------------------------------------------------------------------
# Build pp + start a server on a scratch HOME/port (mirrors test_auto_bootstrap.sh).
# ---------------------------------------------------------------------------
echo "=== building pp-int ==="
rm -f pp-int
mag build -o pp-int >/dev/null
codesign -s - pp-int

echo "=== starting server on port $PORT (HOME=$TMP) ==="
env HOME="$TMP" ./pp-int serve --port "$PORT" >"$LOG" 2>&1 &
SERVER=$!

for i in $(seq 1 12); do
  sleep 1
  curl -s "http://localhost:$PORT/api/health" >/dev/null 2>&1 && break
  kill -0 "$SERVER" 2>/dev/null || fail "server exited early during startup"
done
kill -0 "$SERVER" 2>/dev/null || fail "server not running after startup wait"

# ---------------------------------------------------------------------------
# 1. propose -> capture the fresh dctr-<hex> id.
# ---------------------------------------------------------------------------
echo "=== propose ==="
PROPOSE=$(pp doctrine propose "Guard decoded Dictionary access at the IO boundary" \
  --rationale "at:ifAbsent: on a non-Dictionary throws" \
  --applies global --tags testing,io-boundary --confidence 0.8 --key guard-decoded-dict 2>&1) \
  || fail "propose exited non-zero: $PROPOSE"
echo "$PROPOSE"
assert_contains "$PROPOSE" "proposed doctrine dctr-" "propose did not confirm a fresh dctr- id"
DID=$(printf '%s\n' "$PROPOSE" | grep -oE 'dctr-[0-9a-f]+' | head -1)
[[ -n "$DID" ]] || fail "could not parse doctrine id from propose output"
echo "doctrine id: $DID"

# ---------------------------------------------------------------------------
# 2. list -> entry appears.
# ---------------------------------------------------------------------------
echo "=== list ==="
LIST=$(pp doctrine list 2>&1) || fail "list exited non-zero"
echo "$LIST"
assert_contains "$LIST" "$DID" "list did not show the proposed doctrine"
assert_contains "$LIST" "[proposed]" "list did not render maturity"

# ---------------------------------------------------------------------------
# 3. show -> renders the entry fields.
# ---------------------------------------------------------------------------
echo "=== show ==="
SHOW=$(pp doctrine show "$DID" 2>&1) || fail "show exited non-zero"
echo "$SHOW"
assert_contains "$SHOW" "id:         $DID" "show did not render the id"
assert_contains "$SHOW" "maturity:   proposed" "show did not render proposed maturity"
assert_contains "$SHOW" "crystallized_into: (none)" "show did not render empty crystallized_into"

# ---------------------------------------------------------------------------
# 4. promote -> maturity active.
# ---------------------------------------------------------------------------
echo "=== promote ==="
PROMOTE=$(pp doctrine promote "$DID" 2>&1) || fail "promote exited non-zero"
echo "$PROMOTE"
assert_contains "$PROMOTE" "$DID -> active" "promote did not confirm active"
SHOW=$(pp doctrine show "$DID" 2>&1)
assert_contains "$SHOW" "maturity:   active" "doctrine not active after promote"

# ---------------------------------------------------------------------------
# 5. crystallize --as workitem -> work item created + crystallized_into
#    workitem link recorded; doctrine STAYS active.
#
#    *** THE CRITICAL ASSERTION ***
#    In the shadowed-handler bug, `--as workitem` was routed into the
#    convention handler, which prints "Only --as convention is supported"
#    and creates NO work item. We assert the workitem path reached its OWN
#    logic (emitted "-> workitem wi-crystal-") and was NOT shadowed.
# ---------------------------------------------------------------------------
echo "=== crystallize --as workitem (must NOT be shadowed by convention handler) ==="
CRYS_WI=$(pp doctrine crystallize "$DID" --as workitem --title "Fix IO guard" --type story 2>&1) \
  || fail "crystallize --as workitem exited non-zero: $CRYS_WI"
echo "$CRYS_WI"
assert_contains "$CRYS_WI" "-> workitem wi-crystal-" "workitem crystallize path was SHADOWED (reached the wrong handler)"
assert_not_contains "$CRYS_WI" "Only --as convention is supported" "workitem crystallize fell through to the convention handler"
assert_not_contains "$CRYS_WI" "unsupported crystallization target" "workitem crystallize was rejected"
WIID=$(printf '%s\n' "$CRYS_WI" | grep -oE 'wi-crystal-[0-9a-f]+' | head -1)
[[ -n "$WIID" ]] || fail "could not parse spawned work item id"
echo "work item id: $WIID"

# The work item actually exists in the tuplespace at the storage scope.
WI=$(pp read workitem "$SCOPE" 2>&1) || fail "read workitem exited non-zero"
assert_contains "$WI" "$WIID" "spawned work item not found in tuplespace"

# The doctrine recorded a crystallized_into workitem link AND stays active.
SHOW=$(pp doctrine show "$DID" 2>&1)
echo "$SHOW"
assert_contains "$SHOW" "crystallized_into:" "doctrine did not record a crystallized_into link"
assert_contains "$SHOW" "$WIID" "doctrine crystallized_into does not reference the work item"
assert_contains "$SHOW" "maturity:   active" "doctrine maturity changed after workitem crystallize (must stay active)"

# ---------------------------------------------------------------------------
# 6. crystallize --as convention WITHOUT flags -> REFUSED + prints four clauses.
#    (Run the refusal case first so a stray convention is never written.)
# ---------------------------------------------------------------------------
echo "=== crystallize --as convention WITHOUT flags (must REFUSE) ==="
REFUSE=$(pp doctrine crystallize "$DID" --as convention 2>&1) \
  || fail "crystallize convention (refused) exited non-zero: $REFUSE"
echo "$REFUSE"
assert_contains "$REFUSE" "REFUSED" "convention crystallize without flags was not refused"
assert_contains "$REFUSE" "--affirm-unconditional" "refusal did not name the unconditional clause"
assert_contains "$REFUSE" "--affirm-no-false-positive" "refusal did not name the no-false-positive clause"
assert_contains "$REFUSE" "--affirm-mechanical" "refusal did not name the mechanical clause"
assert_contains "$REFUSE" "--affirm-evidence" "refusal did not name the evidence clause"
# No convention should have been written by the refused attempt.
assert_not_contains "$REFUSE" "-> convention conv-" "refused convention crystallize still wrote a convention"

# ---------------------------------------------------------------------------
# 7. crystallize --as convention WITH all four flags -> pinned convention
#    written (visible to scan: convention), convention link recorded, doctrine
#    stays active.
# ---------------------------------------------------------------------------
echo "=== crystallize --as convention WITH all four --affirm-* flags ==="
CRYS_CONV=$(pp doctrine crystallize "$DID" --as convention \
  --affirm-unconditional --affirm-no-false-positive \
  --affirm-mechanical --affirm-evidence 2>&1) \
  || fail "crystallize convention (affirmed) exited non-zero: $CRYS_CONV"
echo "$CRYS_CONV"
assert_contains "$CRYS_CONV" "-> convention conv-" "affirmed convention crystallize did not write a convention"
assert_contains "$CRYS_CONV" "BINDING" "affirmed convention crystallize did not report a binding write"
CONVID=$(printf '%s\n' "$CRYS_CONV" | grep -oE 'conv-[0-9a-f]+' | head -1)
[[ -n "$CONVID" ]] || fail "could not parse spawned convention id"
echo "convention id: $CONVID"

# The pinned convention is visible via a scoped scan (scan: convention).
CONV=$(pp read convention "$SCOPE" 2>&1) || fail "read convention exited non-zero"
assert_contains "$CONV" "$CONVID" "pinned convention not visible to scan: convention"

# Doctrine recorded the convention link and stays active.
SHOW=$(pp doctrine show "$DID" 2>&1)
echo "$SHOW"
assert_contains "$SHOW" "$CONVID" "doctrine crystallized_into does not reference the convention"
assert_contains "$SHOW" "maturity:   active" "doctrine maturity changed after convention crystallize"

# ---------------------------------------------------------------------------
# 8. decrystallize -> convention retired + crystallized_into link cleared.
# ---------------------------------------------------------------------------
echo "=== decrystallize ==="
DECRYS=$(pp doctrine decrystallize "$DID" --convention "$CONVID" 2>&1) \
  || fail "decrystallize exited non-zero: $DECRYS"
echo "$DECRYS"
assert_contains "$DECRYS" "decrystallized" "decrystallize did not confirm"
# Convention is gone from the tuplespace.
CONV=$(pp read convention "$SCOPE" 2>&1)
assert_not_contains "$CONV" "$CONVID" "convention still present after decrystallize"
# crystallized_into convention link cleared (workitem link remains).
SHOW=$(pp doctrine show "$DID" 2>&1)
assert_not_contains "$SHOW" "$CONVID" "convention link not cleared after decrystallize"
assert_contains "$SHOW" "$WIID" "workitem link was wrongly cleared by decrystallize"

# ---------------------------------------------------------------------------
# 9. retire / reject -> maturity transitions (the other runWith: routes).
# ---------------------------------------------------------------------------
echo "=== retire ==="
RETIRE=$(pp doctrine retire "$DID" 2>&1) || fail "retire exited non-zero"
echo "$RETIRE"
assert_contains "$RETIRE" "$DID -> retired" "retire did not confirm"
SHOW=$(pp doctrine show "$DID" 2>&1)
assert_contains "$SHOW" "maturity:   retired" "doctrine not retired after retire"

echo "=== reject ==="
REJECT=$(pp doctrine reject "$DID" 2>&1) || fail "reject exited non-zero"
echo "$REJECT"
assert_contains "$REJECT" "$DID -> rejected" "reject did not confirm"
SHOW=$(pp doctrine show "$DID" 2>&1)
assert_contains "$SHOW" "maturity:   rejected" "doctrine not rejected after reject"

echo ""
echo "PASS: doctrine CLI end-to-end (every runWith: subcommand dispatched correctly)"
