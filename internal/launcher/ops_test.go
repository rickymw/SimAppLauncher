package launcher

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/config"
)

// These cover the computed half directly. The printer tests in launcher_test.go
// still assert on stdout, which together pin both renderers of the same values.

func opsConfig() config.Config {
	return config.Config{Apps: []config.App{
		{Name: "iRacing", Path: `C:\ir.exe`, ProcessName: "iRacingSim64DX11"},
		{Name: "SimHub", Path: `C:\sh.exe`},
	}}
}

func TestProcessNameFallsBackToDisplayName(t *testing.T) {
	if got := processName(config.App{Name: "SimHub"}); got != "SimHub" {
		t.Errorf("processName without an explicit one = %q, want the display name", got)
	}
	if got := processName(config.App{Name: "iRacing", ProcessName: "iRacingSim64DX11"}); got != "iRacingSim64DX11" {
		t.Errorf("processName = %q, want the explicit one", got)
	}
}

func TestStatusReportsBothStates(t *testing.T) {
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) {
			if name == "iRacingSim64DX11" {
				return 4242, true, nil
			}
			return 0, false, nil
		},
	}

	got := Status(opsConfig(), pm)

	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if got[0].Outcome != OutcomeRunning || got[0].PID != 4242 || !got[0].Running() {
		t.Errorf("iRacing = %+v", got[0])
	}
	if got[1].Outcome != OutcomeStopped || got[1].Running() {
		t.Errorf("SimHub = %+v", got[1])
	}
	if CountRunning(got) != 1 {
		t.Errorf("CountRunning = %d, want 1", CountRunning(got))
	}
}

func TestStatusCarriesTheErrorReason(t *testing.T) {
	pm := &mockPM{
		runningFn: func(string) (int, bool, error) { return 0, false, errors.New("tasklist exploded") },
	}

	got := Status(opsConfig(), pm)

	for _, r := range got {
		if r.Outcome != OutcomeFailed {
			t.Fatalf("outcome = %q, want failed", r.Outcome)
		}
		if r.Err != "tasklist exploded" {
			t.Errorf("err = %q, want the underlying reason", r.Err)
		}
		// A status check that failed must not be counted as running — that
		// would report the rig as up on the strength of an error.
		if r.Running() {
			t.Error("a failed status check counted as running")
		}
	}
}

func TestStartSkipsRunningAndLaunchesTheRest(t *testing.T) {
	var spawned []string
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) {
			return 7, name == "iRacingSim64DX11", nil
		},
		spawnFn: func(app config.App) SpawnResult {
			spawned = append(spawned, app.Name)
			return SpawnResult{PID: 99}
		},
	}

	got := Start(opsConfig(), pm)

	if got[0].Outcome != OutcomeAlreadyRunning || got[0].PID != 7 {
		t.Errorf("iRacing = %+v, want already-running", got[0])
	}
	if got[1].Outcome != OutcomeLaunched || got[1].PID != 99 {
		t.Errorf("SimHub = %+v, want launched", got[1])
	}
	if strings.Join(spawned, ",") != "SimHub" {
		t.Errorf("spawned %v, want only the stopped app", spawned)
	}
	// Both are up afterwards, whether this run started them or not.
	if CountRunning(got) != 2 {
		t.Errorf("CountRunning = %d, want 2", CountRunning(got))
	}
}

func TestStartReportsSpawnFailureAndContinues(t *testing.T) {
	pm := &mockPM{
		runningFn: func(string) (int, bool, error) { return 0, false, nil },
		spawnFn: func(app config.App) SpawnResult {
			if app.Name == "iRacing" {
				return SpawnResult{Err: errors.New("file not found")}
			}
			return SpawnResult{PID: 5}
		},
	}

	got := Start(opsConfig(), pm)

	if got[0].Outcome != OutcomeFailed || got[0].Err != "file not found" {
		t.Errorf("iRacing = %+v", got[0])
	}
	if got[1].Outcome != OutcomeLaunched {
		t.Errorf("a failure ahead of it stopped SimHub launching: %+v", got[1])
	}
}

// A status check that fails must not lead to a spawn — launching a second
// instance of an app that is already running is the failure mode the check
// exists to prevent.
func TestStartDoesNotSpawnWhenTheStatusCheckFails(t *testing.T) {
	spawned := 0
	pm := &mockPM{
		runningFn: func(string) (int, bool, error) { return 0, false, errors.New("no") },
		spawnFn:   func(config.App) SpawnResult { spawned++; return SpawnResult{PID: 1} },
	}

	got := Start(opsConfig(), pm)

	if spawned != 0 {
		t.Errorf("spawned %d apps despite an unreadable status", spawned)
	}
	if !strings.Contains(got[0].Err, "status check failed") {
		t.Errorf("err = %q, want it to say the check failed", got[0].Err)
	}
}

func TestStopReportsEachApp(t *testing.T) {
	pm := &mockPM{
		killFn: func(name string) error {
			if name == "SimHub" {
				return errors.New("access denied")
			}
			return nil
		},
	}

	got := Stop(opsConfig(), pm)

	if got[0].Outcome != OutcomeClosed {
		t.Errorf("iRacing = %+v, want closed", got[0])
	}
	if got[1].Outcome != OutcomeFailed || got[1].Err != "access denied" {
		t.Errorf("SimHub = %+v", got[1])
	}
}

func TestStartHonoursDelay(t *testing.T) {
	// The delay is part of what "start the rig" means — some apps need the one
	// before them up before they will attach — so it must survive the split out
	// of RunStart.
	cfg := config.Config{Apps: []config.App{{Name: "A", Path: "a", DelayMs: 30}}}
	pm := &mockPM{
		runningFn: func(string) (int, bool, error) { return 0, false, nil },
		spawnFn:   func(config.App) SpawnResult { return SpawnResult{PID: 1} },
	}

	start := time.Now()
	Start(cfg, pm)
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Errorf("Start returned after %s, want at least the configured 30ms delay", elapsed)
	}
}
