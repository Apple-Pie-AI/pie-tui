#!/usr/bin/env bash
# Replays Run app end to end with the real pie binary against a FAKE Android
# SDK, a fake Gradle project (in a real git repo) and a stub claude:
#
#   1. first run          - Studio's logic, ZERO agent calls
#   2. second run         - the Gradle scan is cached (build files unchanged)
#   3. a project that needs a flag - the build fails, ONE repair teaches the
#      recipe the flag, the retry succeeds
#   4. next run           - the learned recipe replays with ZERO agent calls
#
# No real emulator, no API cost. Needs go and git.
#   scripts/simulate-run-app.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d -t pie-runapp-sim)"
export PIE_HOME="$WORK/home"
SDK="$WORK/sdk"; APP="$WORK/app"; BIN="$WORK/bin"
mkdir -p "$PIE_HOME" "$SDK/platform-tools" "$SDK/emulator" "$APP/app" "$BIN"
ADBLOG="$WORK/adb.log"; GRADLELOG="$WORK/gradle.log"; CLAUDELOG="$WORK/claude.log"
touch "$CLAUDELOG"

# --- fake SDK: one running emulator ----------------------------------------
cat > "$SDK/platform-tools/adb" <<EOF
#!/bin/sh
echo "\$*" >> "$ADBLOG"
[ "\$1" = devices ] && { echo "List of devices attached"; echo "emulator-5554 device model:sdk_gphone64"; exit 0; }
shift 2
case "\$*" in
"emu avd name") printf 'Pixel_7_API_34\nOK\n' ;;
"shell getprop sys.boot_completed") echo 1 ;;
uninstall*) echo Success ;;
install*) echo Success ;;
"shell cmd package resolve-activity"*) echo "com.acme/com.acme.MainActivity" ;;
"shell am start"*) echo "Status: ok" ;;
"shell pidof"*) echo 4242 ;;
esac
EOF
printf '#!/bin/sh\n[ "$1" = -list-avds ] && echo Pixel_7_API_34\n' > "$SDK/emulator/emulator"
chmod +x "$SDK/platform-tools/adb" "$SDK/emulator/emulator"

# --- fake Gradle project ------------------------------------------------------
cat > "$APP/gradlew" <<EOF
#!/bin/sh
echo "\$*" >> "$GRADLELOG"
case "\$*" in *"tasks --all"*) echo "app:installDebug - Installs the Debug build."; exit 0 ;; esac
if [ -f "$WORK/need-flag" ]; then
  case "\$*" in *-Pacme.ci=true*) ;; *) echo "FAILURE: Build failed: property acme.ci is required"; exit 1 ;; esac
fi
mkdir -p app/build/outputs/apk/debug && touch app/build/outputs/apk/debug/app-debug.apk
printf '{"applicationId":"com.acme","variantName":"debug","elements":[{"outputFile":"app-debug.apk"}]}' > app/build/outputs/apk/debug/output-metadata.json
echo "BUILD SUCCESSFUL"
EOF
chmod +x "$APP/gradlew"
echo 'include(":app")' > "$APP/settings.gradle.kts"
echo 'android { }' > "$APP/app/build.gradle.kts"
echo 'app/build/' > "$APP/.gitignore"
git -C "$APP" init -q && git -C "$APP" add -A && git -C "$APP" -c user.name=s -c user.email=s@s commit -qm init

# --- stub claude: the repair answer ------------------------------------------
cat > "$BIN/claude" <<EOF
#!/bin/sh
echo "repair" >> "$CLAUDELOG"
mkdir -p "$APP/.agent"
printf '%s' '{"status":"ok","gradle_args":["-Pacme.ci=true"],"explanation":"the build requires -Pacme.ci=true","notes":"CI property required"}' > "$APP/.agent/run.json"
echo '{"type":"system","subtype":"init","session_id":"sim"}'
echo '{"type":"result","subtype":"success","result":"done","total_cost_usd":0.12,"session_id":"sim"}'
EOF
chmod +x "$BIN/claude"
export PATH="$BIN:$PATH"

printf 'android_sdk_path = "%s"\ntelemetry_enabled = false\n\n[[repo]]\npath = "%s"\n' "$SDK" "$APP" > "$PIE_HOME/config.toml"
GOTOOLCHAIN=auto go build -C "$ROOT" -o "$WORK/pie" .
PIE="$WORK/pie"
"$PIE" devices use Pixel_7_API_34 >/dev/null

step() { printf '\n\033[36m== %s\033[0m\n' "$*"; }
calls() { wc -l < "$CLAUDELOG" | tr -d ' '; }
check() { if eval "$1"; then echo "  ✓ $2"; else echo "  ✗ $2"; exit 1; fi; }
run() { PIE_RUN_APP_QUIET=1 "$PIE" run-app "$@" && tail -1 "$PIE_HOME/logs/run-app-main.log"; }

step "1. first run - Studio's logic, no agent"
run
check '[ "$(calls)" = 0 ]' "0 agent calls"
check 'grep -q "uninstall com.acme" "$ADBLOG" && grep -q "install -t" "$ADBLOG"' "old app removed, new APK installed"
check 'grep -q "am start -W -n com.acme/com.acme.MainActivity" "$ADBLOG"' "launched the LAUNCHER activity"

step "2. second run - the Gradle scan is cached"
run
check '[ "$(grep -c "tasks --all" "$GRADLELOG")" = 1 ]' "tasks --all ran once in two runs"

step "3. the project now needs a flag - one repair teaches it"
touch "$WORK/need-flag"; touch "$APP/app/build.gradle.kts"
run
check '[ "$(calls)" = 1 ]' "exactly 1 agent call"
check 'grep -q "acme.ci=true" "$PIE_HOME"/run/*.json' "the recipe learned -Pacme.ci=true"

step "4. next run - the learned recipe replays without the agent"
run
check '[ "$(calls)" = 1 ]' "still 1 agent call in total"

printf '\n\033[32mRun app behaved\033[0m (workdir %s)\n' "$WORK"
