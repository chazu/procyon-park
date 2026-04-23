#!/usr/bin/env bash
# pp-supervisor.sh — auto-restart wrapper for `pp serve`.
#
# The pp server has historically vanished silently under load (observed:
# port 7777 closed after ~45min of dispatching, with no process and no
# auto-restart). This wrapper runs pp serve in a loop so a crash — whether
# caught by the in-process crash logger (see Main.mag#startServerGuarded:)
# or a hard VM-level abort — is recovered automatically.
#
# Usage:
#   scripts/pp-supervisor.sh [pp-args...]
#
# Environment:
#   PP_SUPERVISOR_MAX_RESTARTS  max restarts in the burst window (default: 10)
#   PP_SUPERVISOR_BURST_WINDOW  seconds for the burst window        (default: 60)
#   PP_SUPERVISOR_LOG_DIR       log directory (default: $HOME/.pp/logs)
#   PP_BIN                      path to pp binary (default: `command -v pp`)
#
# Logs:
#   $PP_SUPERVISOR_LOG_DIR/pp-serve.log       — rolling combined stdout/stderr
#   $PP_SUPERVISOR_LOG_DIR/pp-serve-<ts>.log  — per-run log (also tee'd live)
#   $PP_SUPERVISOR_LOG_DIR/crash-<epoch>.log  — structured crash dumps
#                                                (written by pp itself)
#
# Exit codes:
#   0   supervisor shut down cleanly (SIGINT/SIGTERM)
#   1   too many restarts in the burst window (circuit breaker tripped)
#   2   misconfiguration (pp binary not found)

set -u

MAX_RESTARTS="${PP_SUPERVISOR_MAX_RESTARTS:-10}"
BURST_WINDOW="${PP_SUPERVISOR_BURST_WINDOW:-60}"
LOG_DIR="${PP_SUPERVISOR_LOG_DIR:-$HOME/.pp/logs}"
PP_BIN="${PP_BIN:-$(command -v pp || true)}"

if [[ -z "$PP_BIN" || ! -x "$PP_BIN" ]]; then
  echo "pp-supervisor: pp binary not found (set PP_BIN or add pp to PATH)" >&2
  exit 2
fi

mkdir -p "$LOG_DIR"
ROLLING_LOG="$LOG_DIR/pp-serve.log"

restarts=()
child_pid=0

cleanup() {
  if [[ "$child_pid" -gt 0 ]] && kill -0 "$child_pid" 2>/dev/null; then
    echo "pp-supervisor: forwarding signal to pp (pid=$child_pid)"
    kill -TERM "$child_pid" 2>/dev/null || true
    wait "$child_pid" 2>/dev/null || true
  fi
  exit 0
}
trap cleanup INT TERM

echo "pp-supervisor: starting (pp=$PP_BIN, log_dir=$LOG_DIR, max=$MAX_RESTARTS/$BURST_WINDOW s)" | tee -a "$ROLLING_LOG"

while true; do
  now=$(date +%s)
  ts=$(date -u +%Y%m%dT%H%M%SZ)
  run_log="$LOG_DIR/pp-serve-$ts.log"

  echo "pp-supervisor: launching pp serve $* (run_log=$run_log)" | tee -a "$ROLLING_LOG"

  # Run pp serve; tee all output to both the per-run log and the rolling log.
  # `set +e` around it so we can observe the exit code.
  set +e
  "$PP_BIN" serve "$@" > >(tee -a "$run_log" "$ROLLING_LOG") 2>&1 &
  child_pid=$!
  wait "$child_pid"
  exit_code=$?
  set -e
  child_pid=0

  echo "pp-supervisor: pp serve exited with code $exit_code at $(date -u +%Y-%m-%dT%H:%M:%SZ)" | tee -a "$ROLLING_LOG"

  if [[ "$exit_code" -eq 0 ]]; then
    echo "pp-supervisor: clean exit — not restarting" | tee -a "$ROLLING_LOG"
    exit 0
  fi

  # Burst-window circuit breaker: if we've restarted more than MAX times in
  # BURST_WINDOW seconds, give up so we don't spin tight forever.
  restarts+=("$now")
  cutoff=$(( now - BURST_WINDOW ))
  pruned=()
  for t in "${restarts[@]}"; do
    if [[ "$t" -ge "$cutoff" ]]; then
      pruned+=("$t")
    fi
  done
  restarts=("${pruned[@]}")

  if [[ "${#restarts[@]}" -gt "$MAX_RESTARTS" ]]; then
    echo "pp-supervisor: ${#restarts[@]} restarts in last ${BURST_WINDOW}s (> $MAX_RESTARTS) — circuit breaker tripped" | tee -a "$ROLLING_LOG"
    exit 1
  fi

  sleep 2
done
