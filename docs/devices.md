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
