package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// `pie devices` is the dashboard's DEVICE section for the shell: the one
// device pie uses, who is using it, and who is next.
func init() {
	c := &cobra.Command{
		Use:   "devices",
		Short: "Show the device pie uses (one device, one agent at a time) and the queue",
		RunE: func(_ *cobra.Command, _ []string) error {
			return withDeviceStore(printDevices)
		},
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "use <avd|serial>",
			Short: "Make an AVD or a connected phone the device pie uses",
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				return withDeviceStore(func(st *store.Store, p emulator.SDKPaths) error {
					return useDevice(st, p, args[0])
				})
			},
		},
		&cobra.Command{
			Use:   "start",
			Short: "Start the active emulator (windowed) and wait for it to boot",
			RunE: func(_ *cobra.Command, _ []string) error {
				return withDeviceStore(startDevice)
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the active emulator (refused while an agent is using it)",
			RunE: func(_ *cobra.Command, _ []string) error {
				return withDeviceStore(stopDevice)
			},
		},
	)
	rootCmd.AddCommand(c)
}

func withDeviceStore(fn func(*store.Store, emulator.SDKPaths) error) error {
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	st, err := store.Open(paths.StateDB())
	if err != nil {
		return err
	}
	defer st.Close()
	sdk := ""
	if cfg, err := config.Load(); err == nil {
		sdk = cfg.AndroidSDKPath
		_ = st.SeedDevice(cfg.AVDName)
	}
	return fn(st, emulator.ResolvePaths(sdk))
}

func printDevices(st *store.Store, p emulator.SDKPaths) error {
	d, err := st.ActiveDevice()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer w.Flush()
	if d.None() {
		fmt.Fprintln(w, "DEVICE\tnone - `pie devices use <avd|serial>`")
	} else {
		fmt.Fprintf(w, "DEVICE\t%s (%s)\n", d.Label(), d.Kind)
		fmt.Fprintf(w, "STATE\t%s\n", deviceStateWord(d))
		if d.Serial != "" {
			fmt.Fprintf(w, "SERIAL\t%s\n", d.Serial)
		}
	}
	if d.Holder != "" && d.Holder != store.HolderYou {
		if s, err := st.Get(d.Holder); err == nil && s != nil {
			fmt.Fprintf(w, "AGENT\t%s · %s\n", s.Ticket, s.Summary)
			fmt.Fprintf(w, "BRANCH\t%s\n", s.Branch)
			wt := s.Worktree
			if wt == "" {
				wt = paths.WorktreeFor(s.Repo, s.Ticket)
			}
			fmt.Fprintf(w, "WORKTREE\t%s\n", wt)
		}
	}
	if ws, _ := st.DeviceWaiters(); len(ws) > 0 {
		var q []string
		for _, wt := range ws {
			if wt.Ticket != d.Holder {
				q = append(q, wt.Ticket)
			}
		}
		if len(q) > 0 {
			fmt.Fprintf(w, "QUEUE\t%s\n", strings.Join(q, " → "))
		}
	}
	if avds := emulator.ListAVDs(p); len(avds) > 0 {
		fmt.Fprintf(w, "AVDS\t%s\n", strings.Join(avds, ", "))
	}
	if devs, err := emulator.ListDevices(p); err == nil {
		for _, dv := range devs {
			if dv.Kind == emulator.KindUSB {
				fmt.Fprintf(w, "PHONE\t%s %s (%s)\n", dv.Serial, dv.Model, dv.State)
			}
		}
	}
	return nil
}

func deviceStateWord(d store.Device) string {
	switch {
	case d.State == store.DeviceStarting:
		return "starting"
	case d.State != store.DeviceRunning:
		return "off"
	case d.Holder == store.HolderYou:
		return "running · reserved by you"
	case d.Holder != "":
		return "running · in use by " + d.Holder
	}
	return "running · free"
}

// useDevice picks an AVD by name or a connected phone by serial.
func useDevice(st *store.Store, p emulator.SDKPaths, name string) error {
	for _, avd := range emulator.ListAVDs(p) {
		if avd == name {
			serial, _ := emulator.SerialForAVD(p, avd)
			return reportBusy(st.UseEmulator(avd, serial), "pie now uses "+avd)
		}
	}
	devs, err := emulator.ListDevices(p)
	if err != nil {
		return fmt.Errorf("adb: %w", err)
	}
	for _, dv := range devs {
		if dv.Serial == name && dv.Kind == emulator.KindUSB {
			if !dv.Ready() {
				return fmt.Errorf("%s is %s - accept USB debugging on the phone", name, dv.State)
			}
			return reportBusy(st.UseUSB(name), "pie now uses the phone "+name)
		}
	}
	return fmt.Errorf("no AVD or connected phone named %q (see `pie devices`)", name)
}

func reportBusy(err error, ok string) error {
	if errors.Is(err, store.ErrDeviceBusy) {
		return errors.New("an agent is using the device - wait for it, or take it back in the dashboard")
	}
	if err == nil {
		fmt.Println(ok)
	}
	return err
}

func startDevice(st *store.Store, p emulator.SDKPaths) error {
	d, err := st.ActiveDevice()
	if err != nil {
		return err
	}
	if d.Kind != store.DeviceEmulator {
		return errors.New("the active device is not an emulator - `pie devices use <avd>` first")
	}
	if won, _ := st.BeginStart(d.AVD, 0); won {
		pid, _, err := emulator.StartWindowed(p, d.AVD, emulator.StartOpts{LogPath: paths.EmulatorLog()})
		if err != nil {
			_ = st.MarkOff()
			return err
		}
		_ = st.SetStartPID(pid)
		fmt.Printf("starting %s (windowed)…\n", d.AVD)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	serial, err := emulator.WaitAVDReady(ctx, p, d.AVD)
	if err != nil {
		return err
	}
	if err := st.MarkRunning(d.AVD, serial); err != nil {
		return err
	}
	fmt.Printf("%s is running (%s)\n", d.AVD, serial)
	return nil
}

func stopDevice(st *store.Store, p emulator.SDKPaths) error {
	d, err := st.ActiveDevice()
	if err != nil {
		return err
	}
	if d.Holder != "" && d.Holder != store.HolderYou {
		return fmt.Errorf("%s's agent is using %s - take it back in the dashboard first", d.Holder, d.Label())
	}
	if d.Kind != store.DeviceEmulator || d.State == store.DeviceOff {
		return errors.New("no running emulator to stop")
	}
	if d.Serial != "" {
		if err := emulator.StopSerial(p, d.Serial); err != nil {
			return err
		}
	} else {
		emulator.Kill(d.PID)
	}
	fmt.Printf("stopped %s\n", d.AVD)
	return st.MarkOff()
}
