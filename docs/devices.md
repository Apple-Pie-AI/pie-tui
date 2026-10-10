# Devices

Apple Pie works with **one device at a time, and one agent uses it at a time**. That device is an emulator or a USB phone. It's always on the dashboard, in the **DEVICE** section between the command rows and NEEDS YOU.

Emulators always open **in a window**, for agents too, so you can watch what an agent does to the app. Apple Pie never starts a second emulator and never touches a device that isn't the active one.

## The DEVICE section

There is one row. Put the cursor on it and the right-hand pane shows the device and an **ACTIONS** list. **Enter** moves into the actions, **↑↓** picks one, **Enter** runs it, and **Esc** goes back to the list. The header (top right) always shows the same state in a few words.

| State | The row says | Actions |
|---|---|---|
| No device / off | `No device running · off` | Start `<avd>` · Start a different emulator · Use a connected phone |
| Starting | `starting… 96%` | Cancel start |
| Running, free | `no agent is using it · running · free` | Show emulator window · Restart · Switch to a different device · Stop |
| Running, used by an agent | `PLEX-12 · … · in use by agent`, with a second line `branch <branch>` | Open ticket · Show emulator window · Take it back from the agent · Stop |
| Reserved by you | `reserved by you` | Release to agents · Show window · Switch · Stop |
| A USB phone | `Pixel 7 (USB) · connected` | Switch to a different device · Stop using this phone |

The detail pane fills in more:

- **Device:** the device model, its image (`API 34 · Google Play`) and how long it has been up.
- **AGENT** (when an agent holds it): the ticket, its **branch**, its **worktree**, the stage, and how long it has had the device.
- **NEXT IN LINE:** the first ticket queued for the device.

The boot progress is an **estimate**: elapsed time over how long that AVD took to boot last time. A first boot shows elapsed time instead.

Ticket rows show their part in this. The holder reads **`verifying on device`** and a queued ticket reads **`waiting for device`**.

## One agent at a time

When a ticket's verify needs a device, the agent behaves like this:

1. **It joins the queue.** Tickets are served first come, first served. Only the oldest *live* waiter can take the device, and a waiter whose run died is skipped.
2. **It brings the device up if needed.** An emulator that's off is launched (windowed). An emulator already running for **that AVD** is adopted, even if you started it yourself. A USB phone must be connected and authorized.
3. **It gets the device's serial.** `ANDROID_SERIAL` is set for it, and its prompt names the serial and says that every other attached device belongs to you.
   - If an install fails for lack of storage, or because the app is already installed with a different signature, the agent uninstalls the old version first.
4. **It hands the device back when verify ends.**

If the device doesn't come up, or the ticket's turn doesn't come within 20 minutes, verify runs **unit tests only**. A missing device never fails a ticket.

**Take it back from the agent** stops that agent's verify and parks its ticket in NEEDS YOU ("device taken back"), and the device becomes **reserved by you**. Choose **Resume** on the ticket to put it back in the queue, and **Release to agents** when you're done.

Destructive actions (Stop while an agent is testing, Take it back) ask first, with **Cancel** selected. You can't switch devices while an agent holds the device; take it back first. Switching away from an emulator Apple Pie launched asks whether to stop it.

## Run app

Choose **▶ Run app on this device** in the DEVICE actions, or **▶ Run this ticket's app** in a ticket's menu. Apple Pie then builds, installs and launches the app on the active device, the way Android Studio's Run button does.

The screen has three steps; **Esc** goes back one step:

1. **Code:** the main checkout (your local edits included) or any ticket that has a worktree. When you start from a ticket, this step is skipped.
2. **Variant:** Studio's Build Variants. The last variant you used is preselected, and release variants are dimmed because they need signing. **Sync project** rescans the Gradle tasks, which also happens automatically whenever a build file changes.
3. **Progress:** Prepare → Device → Sync → Build → Reinstall → Launch, with the log underneath.
   - **Back** leaves the run going in the background. It's a separate `pie run-app` process, so closing the dashboard doesn't stop it either.
   - **Stop** stops it, and **Run again** reruns it.

### How it runs the app (no agent needed)

Studio doesn't need an AI to run a project, and neither does Apple Pie. Each Studio step has a terminal equivalent:

| Step | What runs |
|---|---|
| App modules and variants | `./gradlew tasks --all`. Only app modules have `install<Variant>` tasks. The result is cached until a build file changes |
| Build | `./gradlew :<module>:assemble<Variant>`, with the same JDK and SDK the agents use, and `ANDROID_SERIAL` set |
| applicationId and APK | `build/outputs/apk/**/output-metadata.json`, written by the Android Gradle Plugin |
| Reinstall | `adb uninstall <appId>` first, then `adb install`. Removing the old version avoids "insufficient storage" and signature-mismatch failures, **but it clears the app's data** on the device |
| Launch | `cmd package resolve-activity` finds the LAUNCHER activity, then `am start -W`. An app that exits within 2 seconds is reported as a crash, and its error log goes into the run log |

### When a step fails: one repair, then remembered

If a step fails in a way that's about **how** the app is run (the build needs a flag, the Gradle root is elsewhere, there's no launcher activity, the wrong JDK), Apple Pie asks an agent **once**. It tells the agent exactly which command failed and shows the last lines of its output.

The agent can only **read** the project. It gets a narrow allowlist, and edits are denied. It answers in one of three ways:

- **ok:** with corrections (a Gradle root, flags, `JAVA_HOME` / `GRADLE_OPTS`, or the build task, applicationId or launch activity for one variant). They're validated, saved, and the run retries once.
- **source_error:** the project's own code is broken. You get the agent's explanation, and nothing is saved.
- **blocked:** a human is needed. You get the explanation and a suggested command, and Apple Pie never runs it.

If a repair session changes any project file, its answer is rejected and the files are left as they are for you to inspect.

Whatever a repair teaches is saved in the **run recipe**, `~/.pie/run/<repo>-<hash>.json`, and **every later run replays it without the agent**. Delete that file to start fresh. Repairs use `model_run` and `max_budget_run_usd` (see [configuration.md](configuration.md)).

Run app takes the device as **you**. If an agent is testing on it, you're asked to take it back first (Cancel is selected).

From the shell: `pie run-app [--target main|TICKET] [--module :app] [--variant stagingDebug] [--sync] [--no-repair]`.

`scripts/simulate-run-app.sh` (`make simulate-run-app`) replays four runs with a fake SDK and project:

1. A first run with no agent call.
2. A run with the Gradle scan cached.
3. A run where one repair teaches a missing flag.
4. A run that replays the learned flag with no agent.

## Choosing the device

- **First run:** the active device starts out as config `avd_name`, or the first AVD `emulator -list-avds` reports. After that it's whatever you pick in the dashboard or with `pie devices use`.
- **Stopping:** the cleanup daemon **never** stops the device; you're in control of it. `emulator_idle_timeout` is no longer used.

## From the shell

```bash
pie devices                    # the active device, who uses it (ticket, branch, worktree), the queue
pie devices use Pixel_7_API_34 # or a phone's serial
pie devices start              # launch it windowed and wait for the boot
pie devices stop               # refused while an agent is using it
```

`scripts/simulate-device.sh` replays the whole flow against a fake SDK, with no real emulator and no cost: pick, start, an agent takes the device while another ticket queues, stop is refused, release, stop.
