#!/usr/bin/env bash
# QA-launch THIS checkout: build the branch you are standing in and run it
# against the isolated bench home, so what you test is exactly this worktree's
# code - never a stale hand-built binary.
#
#   scripts/qa.sh                 → build + open the TUI (bench home)
#   scripts/qa.sh run <ticket.md> → build + run a ticket headless
#   scripts/qa.sh logs <TICKET>   → any pie subcommand passes through
#
# The bench home defaults to the seeding-QA regression home; override with
# PIE_QA_HOME. The binary lands in dist/qa/ (gitignored with the rest of dist).
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

BENCH="${PIE_QA_HOME:-$HOME/Documents/pie-seeding-test/regression-home}"
BIN="$PWD/dist/qa/pie"

mkdir -p dist/qa
GOTOOLCHAIN=auto go build -o "$BIN" .
echo "qa: built $(git rev-parse --abbrev-ref HEAD) @ $(git rev-parse --short HEAD)"
echo "qa: PIE_HOME=$BENCH"
PIE_HOME="$BENCH" exec "$BIN" "$@"
