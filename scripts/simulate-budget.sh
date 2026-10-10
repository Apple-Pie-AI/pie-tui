#!/usr/bin/env bash
# Live simulation of the budget question, end to end, with the REAL claude.
#
# Builds pie, creates a throwaway repo and PIE_HOME, gives ONE stage a budget
# far too small to finish on, and runs `pie run --dry-run` against it (nothing
# is ever pushed). It then plays the dashboard: every budget question that
# appears in the approvals table is answered the way the TUI's overlay writes
# it. Both kinds of budget stop end up here - the CLI's hard stop and an agent
# that quits unfinished near its limit.
#
#   scripts/simulate-budget.sh                 # plan stage, answer Continue
#   scripts/simulate-budget.sh stop            # answer Stop on the first question
#   STAGE=impl scripts/simulate-budget.sh      # the implement stage (PLEX-64819's)
#   BUDGET=0.05 MODEL=sonnet scripts/simulate-budget.sh
#
# STAGE=plan stops at plan review (--review-plan). STAGE=impl runs on through
# implement and verify and stops at the review-before-PR gate; the throwaway
# repo has no build, so verify may well end at needs-you - the budget lines
# are what this script is for.
#
# Costs real tokens (a few cents on haiku). Needs claude, git and sqlite3.
set -euo pipefail

MODE="${1:-continue}"
STAGE="${STAGE:-plan}"
BUDGET="${BUDGET:-0.02}"
MODEL="${MODEL:-haiku}"
case "$STAGE" in
  plan) BUDGET_KEY=max_budget_plan_usd; MODEL_KEY=model_plan; EXTRA_FLAGS=(--review-plan) ;;
  impl) BUDGET_KEY=max_budget_impl_usd; MODEL_KEY=model_impl; EXTRA_FLAGS=() ;;
  *) echo "STAGE must be plan or impl" >&2; exit 1 ;;
esac
MAX_ROUNDS="${MAX_ROUNDS:-15}"

for bin in claude git sqlite3 go; do
  command -v "$bin" >/dev/null || { echo "missing: $bin" >&2; exit 1; }
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d -t pie-budget-sim)"
export PIE_HOME="$WORK/pie-home"
mkdir -p "$PIE_HOME"
echo "workdir: $WORK"

GOTOOLCHAIN=auto go build -C "$ROOT" -o "$WORK/pie" .

# A tiny repo with a bare origin (the worktree step fetches from it).
git init -q --bare "$WORK/origin.git"
git init -q "$WORK/app"
cat > "$WORK/app/MainActivity.kt" <<'EOF'
package com.example.app

class MainActivity {
    fun greeting(): String = "Hello"
}
EOF
git -C "$WORK/app" add -A
git -C "$WORK/app" -c user.name=sim -c user.email=sim@sim commit -qm init
git -C "$WORK/app" branch -M main
git -C "$WORK/app" remote add origin "$WORK/origin.git"
git -C "$WORK/app" push -qu origin main

cat > "$PIE_HOME/config.toml" <<EOF
$MODEL_KEY = "$MODEL"
max_budget_usd = 5
$BUDGET_KEY = $BUDGET
telemetry_enabled = false

[[repo]]
path = "$WORK/app"
branch = "{ticket}"
EOF

cat > "$WORK/budget-demo.md" <<'EOF'
# Personalize the greeting

Change MainActivity.greeting() to take a name and return "Hello, <name>!".
Update any callers.
EOF

DB="$PIE_HOME/state.db"
LOG="$WORK/run.out"
echo "$STAGE budget: \$$BUDGET on $MODEL · answering: $MODE"
"$WORK/pie" run "$WORK/budget-demo.md" --dry-run ${EXTRA_FLAGS[@]+"${EXTRA_FLAGS[@]}"} >"$LOG" 2>&1 &
PID=$!

rounds=0
while kill -0 "$PID" 2>/dev/null; do
  row="$(sqlite3 "$DB" "SELECT id || '|' || command FROM approvals WHERE state='pending' AND tool='Budget' ORDER BY id LIMIT 1;" 2>/dev/null || true)"
  if [ -n "$row" ]; then
    id="${row%%|*}"
    rounds=$((rounds + 1))
    echo "⏸ dashboard: ${row#*|}"
    if [ "$MODE" = "stop" ] || [ "$rounds" -gt "$MAX_ROUNDS" ]; then
      echo "  → Stop"
      sqlite3 "$DB" "UPDATE approvals SET state='denied', decided_at=strftime('%s','now') WHERE id=$id AND state='pending';"
    else
      echo "  → Continue"
      sqlite3 "$DB" "UPDATE approvals SET state='allowed', decided_at=strftime('%s','now') WHERE id=$id AND state='pending';"
    fi
  fi
  sleep 0.5
done
wait "$PID" || true

echo
echo "── budget lines from the ticket log ──"
"$WORK/pie" logs BUDGET-DEMO 2>/dev/null | grep -E "budget|stage:|plan-review|change-review|needs-you|spending|ended in state" || true
echo
echo "budget questions asked: $rounds"
echo "final state: $(sqlite3 "$DB" "SELECT state FROM sessions WHERE ticket='BUDGET-DEMO';")"
if [ -f "$PIE_HOME/plans/BUDGET-DEMO.md" ]; then
  echo "plan written: $PIE_HOME/plans/BUDGET-DEMO.md"
fi
