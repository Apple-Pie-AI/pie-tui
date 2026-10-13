package store

import "testing"

// Two Gradle builds in one checkout collide: a second live run for the same
// directory is refused; a finished or dead one doesn't block.
func TestAppRunsOnePerDir(t *testing.T) {
	st := deviceStore(t)
	alive := func(pid int) bool { return pid == 1 }
	if err := st.CreateAppRun(AppRun{ID: "a", Target: "main", Dir: "/repo", PID: 1}, alive); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAppRun(AppRun{ID: "b", Target: "main", Dir: "/repo", PID: 2}, alive); err != ErrAppRunBusy {
		t.Fatalf("second live run = %v, want ErrAppRunBusy", err)
	}
	if err := st.CreateAppRun(AppRun{ID: "c", Target: "PLEX-1", Dir: "/wt/PLEX-1", PID: 2}, alive); err != nil {
		t.Fatalf("another checkout must not be blocked: %v", err)
	}
	_ = st.SetAppRunStep("a", "build", "./gradlew :app:assembleDebug")
	_ = st.SetAppRunChoice("a", ":app", "debug", "emulator-5554")
	_ = st.FinishAppRun("a", AppRunDone, "com.acme running", true)
	r, err := st.GetAppRun("a")
	if err != nil || r.State != AppRunDone || r.Step != "build" || r.Variant != "debug" || !r.Repaired {
		t.Fatalf("run = %+v, %v", r, err)
	}
	if err := st.CreateAppRun(AppRun{ID: "d", Target: "main", Dir: "/repo", PID: 3}, alive); err != nil {
		t.Fatalf("a finished run must not block: %v", err)
	}
	if latest, _ := st.LatestAppRun(); latest.ID != "d" {
		t.Errorf("latest = %q", latest.ID)
	}
}
