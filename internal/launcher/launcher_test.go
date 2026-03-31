package launcher

import (
	"errors"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/config"
)

// mockPM is a test double for ProcessManager.
type mockPM struct {
	spawnFn   func(app config.App) SpawnResult
	runningFn func(name string) (int, bool, error)
	killFn    func(name string) error
}

func (m *mockPM) Spawn(app config.App) SpawnResult             { return m.spawnFn(app) }
func (m *mockPM) IsRunning(name string) (int, bool, error)     { return m.runningFn(name) }
func (m *mockPM) Kill(name string) error                       { return m.killFn(name) }

func twoAppConfig() config.Config {
	return config.Config{
		Apps: []config.App{
			{Name: "AppA", ProcessName: "appa", DelayMs: 0},
			{Name: "AppB", ProcessName: "appb", DelayMs: 0},
		},
	}
}

// TestRunStart_AllSucceed verifies that all apps are launched and the summary is correct.
func TestRunStart_AllSucceed(t *testing.T) {
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) { return 0, false, nil },
		spawnFn:   func(app config.App) SpawnResult { return SpawnResult{PID: 100} },
	}

	out := captureStdout(func() { RunStart(twoAppConfig(), pm) })

	if !strings.Contains(out, "AppA") || !strings.Contains(out, "AppB") {
		t.Errorf("expected both app names in output, got: %q", out)
	}
	if !strings.Contains(out, "2/2") {
		t.Errorf("expected 2/2 in summary, got: %q", out)
	}
}

// TestRunStart_OneFails verifies that a failed launch is reported and the summary reflects it.
func TestRunStart_OneFails(t *testing.T) {
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) { return 0, false, nil },
		spawnFn: func(app config.App) SpawnResult {
			if app.Name == "AppB" {
				return SpawnResult{Err: errors.New("path not found")}
			}
			return SpawnResult{PID: 100}
		},
	}

	out := captureStdout(func() { RunStart(twoAppConfig(), pm) })

	if !strings.Contains(out, "[!]") {
		t.Errorf("expected failure marker [!] in output, got: %q", out)
	}
	if !strings.Contains(out, "1/2") {
		t.Errorf("expected 1/2 in summary, got: %q", out)
	}
}

// TestRunStart_AlreadyRunning verifies that already-running apps are skipped and counted as running.
func TestRunStart_AlreadyRunning(t *testing.T) {
	spawnCalled := 0
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) { return 9999, true, nil },
		spawnFn: func(app config.App) SpawnResult {
			spawnCalled++
			return SpawnResult{PID: 100}
		},
	}

	out := captureStdout(func() { RunStart(twoAppConfig(), pm) })

	if spawnCalled != 0 {
		t.Errorf("Spawn called %d times, want 0", spawnCalled)
	}
	if !strings.Contains(out, "[=]") {
		t.Errorf("expected [=] already-running marker in output, got: %q", out)
	}
	if !strings.Contains(out, "2/2") {
		t.Errorf("expected 2/2 in summary (already running counts), got: %q", out)
	}
}

// TestRunStart_IsRunningError verifies that an IsRunning error is reported and app is skipped.
func TestRunStart_IsRunningError(t *testing.T) {
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) {
			return 0, false, errors.New("tasklist failed")
		},
		spawnFn: func(app config.App) SpawnResult { return SpawnResult{PID: 100} },
	}

	out := captureStdout(func() { RunStart(twoAppConfig(), pm) })

	if !strings.Contains(out, "[!]") {
		t.Errorf("expected failure marker [!] in output, got: %q", out)
	}
}

// TestRunStop_AllClose verifies that all apps are closed successfully.
func TestRunStop_AllClose(t *testing.T) {
	pm := &mockPM{
		killFn: func(name string) error { return nil },
	}

	out := captureStdout(func() { RunStop(twoAppConfig(), pm) })

	if !strings.Contains(out, "AppA") || !strings.Contains(out, "AppB") {
		t.Errorf("expected both app names in output, got: %q", out)
	}
	if strings.Contains(out, "[!]") {
		t.Errorf("unexpected failure in output: %q", out)
	}
}

// TestRunStop_OneFails verifies that a kill failure is reported without stopping the rest.
func TestRunStop_OneFails(t *testing.T) {
	pm := &mockPM{
		killFn: func(name string) error {
			if name == "appa" {
				return errors.New("access denied")
			}
			return nil
		},
	}

	out := captureStdout(func() { RunStop(twoAppConfig(), pm) })

	if !strings.Contains(out, "[!]") {
		t.Errorf("expected failure marker [!] in output, got: %q", out)
	}
	if !strings.Contains(out, "[-]") {
		t.Errorf("expected success marker [-] for AppB, got: %q", out)
	}
}

// TestRunStatus_Mixed verifies running/stopped state is shown correctly.
func TestRunStatus_Mixed(t *testing.T) {
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) {
			if name == "appa" {
				return 9999, true, nil
			}
			return 0, false, nil
		},
	}

	out := captureStdout(func() { RunStatus(twoAppConfig(), pm) })

	if !strings.Contains(out, "RUNNING") {
		t.Errorf("expected RUNNING in output, got: %q", out)
	}
	if !strings.Contains(out, "STOPPED") {
		t.Errorf("expected STOPPED in output, got: %q", out)
	}
	if !strings.Contains(out, "9999") {
		t.Errorf("expected pid 9999 in output, got: %q", out)
	}
}

// TestRunStatus_IsRunningError verifies that an IsRunning error shows ERROR state.
func TestRunStatus_IsRunningError(t *testing.T) {
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) {
			return 0, false, errors.New("tasklist failed")
		},
	}

	out := captureStdout(func() { RunStatus(twoAppConfig(), pm) })

	if !strings.Contains(out, "ERROR") {
		t.Errorf("expected ERROR in output, got: %q", out)
	}
}

// TestRunStatus_ProcessNameFallback verifies that app.Name is used when ProcessName is empty.
func TestRunStatus_ProcessNameFallback(t *testing.T) {
	cfg := config.Config{
		Apps: []config.App{{Name: "MyApp", ProcessName: ""}},
	}

	var queriedName string
	pm := &mockPM{
		runningFn: func(name string) (int, bool, error) {
			queriedName = name
			return 0, false, nil
		},
	}

	captureStdout(func() { RunStatus(cfg, pm) })

	if queriedName != "MyApp" {
		t.Errorf("IsRunning called with %q, want %q", queriedName, "MyApp")
	}
}
