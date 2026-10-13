#!/usr/bin/env bash
# Replays the device flow end to end against a FAKE Android SDK (stub adb and
# emulator), a throwaway PIE_HOME and the real pie binary + state DB:
#
#   pick a device → start it (windowed launch) → running · free →
#   an agent takes it, a second ticket queues → stop is refused while held →
#   the agent releases → stop works.
#
# No real emulator, no cost. Needs go and sqlite3.
#   scripts/simulate-device.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d -t pie-device-sim)"
export PIE_HOME="$WORK/home"
SDK="$WORK/sdk"
mkdir -p "$PIE_HOME" "$SDK/platform-tools" "$SDK/emulator"
LOG="$WORK/sdk.log"
BOOTED="$WORK/booted"

cat > "$SDK/platform-tools/adb" <<EOF
#!/bin/sh
echo "adb \$*" >> "$LOG"
if [ "\$1" = "devices" ]; then
  echo "List of devices attached"
  [ -f "$BOOTED" ] && echo "emulator-5554 device model:sdk_gphone64"
  exit 0
fi
case "\$*" in
"-s emulator-5554 emu avd name") printf 'Pixel_7_API_34\nOK\n' ;;
"-s emulator-5554 shell getprop sys.boot_completed") echo 1 ;;
"-s emulator-5554 emu kill") rm -f "$BOOTED"; echo "OK: killing emulator, bye bye" ;;
esac
EOF
cat > "$SDK/emulator/emulator" <<EOF
#!/bin/sh
[ "\$1" = "-list-avds" ] && { echo Pixel_7_API_34; echo Pixel_Tablet_API_34; exit 0; }
echo "emulator \$*" >> "$LOG"
touch "$BOOTED"
sleep 2
EOF
chmod +x "$SDK/platform-tools/adb" "$SDK/emulator/emulator"
printf 'android_sdk_path = "%s"\ntelemetry_enabled = false\n' "$SDK" > "$PIE_HOME/config.toml"

GOTOOLCHAIN=auto go build -C "$ROOT" -o "$WORK/pie" .
PIE="$WORK/pie"
DB="$PIE_HOME/state.db"
step() { printf '\n\033[36m== %s\033[0m\n' "$*"; }
expect() { grep -q -- "$1" <<<"$2" || { echo "FAIL: expected '$1' in:"; echo "$2"; exit 1; }; }

step "1. no device chosen yet"
out=$("$PIE" devices); echo "$out"; expect "none" "$out"

step "2. pie devices use Pixel_7_API_34"
"$PIE" devices use Pixel_7_API_34

step "3. pie devices start  (windowed launch - no -no-window)"
"$PIE" devices start
grep -q "emulator -avd Pixel_7_API_34" "$LOG" && ! grep -q -- "-no-window" "$LOG" && echo "launched windowed ✓"
out=$("$PIE" devices); echo "$out"; expect "running · free" "$out"

step "4. an agent (PLEX-64818) takes the device; PLEX-64905 queues behind it"
now=$(date +%s)
sqlite3 "$DB" "INSERT INTO sessions (ticket, repo, state, branch, summary, pid, created_at, updated_at, device_wait_since)
  VALUES ('PLEX-64818','/r','building','gerardosuarez/ai_task-and-notes/layer-14-task-form-ui','PR 14: task-form-ui',$$,$now,$now,0),
         ('PLEX-64905','/r','building','layer-15','Empty state for notes tab',$$,$now,$now,$((now+1)));
  UPDATE active_device SET holder='PLEX-64818', acquired_at=$now WHERE id=1;"
out=$("$PIE" devices); echo "$out"
expect "in use by PLEX-64818" "$out"; expect "layer-14-task-form-ui" "$out"; expect "PLEX-64905" "$(grep QUEUE <<<"$out")"

step "5. one agent at a time: stop is refused while PLEX-64818 uses it"
if "$PIE" devices stop 2>"$WORK/err"; then echo "FAIL: stop should be refused"; exit 1; fi
cat "$WORK/err"

step "6. the agent releases; stop works"
sqlite3 "$DB" "UPDATE active_device SET holder='' WHERE id=1;"
"$PIE" devices stop
out=$("$PIE" devices); echo "$out"; expect "off" "$out"

printf '\n\033[32mall device steps behaved\033[0m (workdir %s)\n' "$WORK"
