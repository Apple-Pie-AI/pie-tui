#!/usr/bin/env bash
# Live simulation of the budget question, end to end, with the REAL claude.
#
# Builds pie, creates a throwaway repo and PIE_HOME, gives the plan stage a
# budget far too small to finish on, and runs `pie run --dry-run --review-plan`
# (nothing is pushed, nothing is implemented - the run ends at plan review).
# It then plays the dashboard: every budget question that appears in the
# approvals table is answered the way the TUI's overlay writes it.
#
#   scripts/simulate-budget.sh            # answer Continue until the plan is done
#   scripts/simulate-budget.sh stop       # answer Stop on the first question
#   BUDGET=0.05 MODEL=sonnet scripts/simulate-budget.sh
#
# Costs real tokens (a few cents on haiku). Needs claude, git and sqlite3.
set -euo pipefail

MODE="${1:-continue}"
BUDGET="${BUDGET:-0.02}"
MODEL="${MODEL:-haiku}"
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
model_plan = "$MODEL"
max_budget_usd = 5
max_budget_plan_usd = $BUDGET
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
echo "plan budget: \$$BUDGET on $MODEL · answering: $MODE"
"$WORK/pie" run "$WORK/budget-demo.md" --dry-run --review-plan >"$LOG" 2>&1 &
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
"$WORK/pie" logs BUDGET-DEMO 2>/dev/null | grep -E "budget|stage:plan|plan-review|needs-you|spending" || true
echo
echo "budget questions asked: $rounds"
echo "final state: $(sqlite3 "$DB" "SELECT state FROM sessions WHERE ticket='BUDGET-DEMO';")"
if [ -f "$PIE_HOME/plans/BUDGET-DEMO.md" ]; then
  echo "plan written: $PIE_HOME/plans/BUDGET-DEMO.md"
fi
