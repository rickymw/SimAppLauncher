//go:build e2e

package launcher

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/SimAppLauncher/internal/config"
)

const (
	startupWait  = 10 * time.Second // time for apps to appear in tasklist after launch
	shutdownWait = 5 * time.Second  // time for apps to disappear after stop
)

// repoRoot resolves the repository root relative to this source file,
// so the test works regardless of which directory go test is run from.
func repoRoot() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "../..")
}

// TestE2E_FullStack launches all configured sim racing apps, verifies each is
// running, stops them all, then verifies each is stopped.
//
// Run with: go test -tags e2e -v ./internal/launcher/ -run TestE2E_FullStack -timeout 60s
// WARNING: this will launch and close your actual sim racing apps.
func TestE2E_FullStack(t *testing.T) {
	cfgPath := filepath.Join(repoRoot(), "launcher.config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config from %s: %v", cfgPath, err)
	}

	pm := NewProcessManager()

	// Always stop all apps on exit, even if the test fails mid-way.
	t.Cleanup(func() {
		t.Log("cleanup: stopping all apps")
		captureStdout(func() { RunStop(cfg, pm) })
	})

	// 1. Start all apps.
	t.Log("starting all apps...")
	out := captureStdout(func() { RunStart(cfg, pm) })
	t.Log(out)

	t.Logf("waiting %s for apps to initialize...", startupWait)
	time.Sleep(startupWait)

	// 2. Verify each app is running.
	t.Log("verifying apps are running...")
	for _, app := range cfg.Apps {
		name := app.ProcessName
		if name == "" {
			name = app.Name
		}
		pid, running, err := pm.IsRunning(name)
		if err != nil {
			t.Errorf("[FAIL] %s — IsRunning error: %v", app.Name, err)
		} else if !running {
			t.Errorf("[FAIL] %s — not found in tasklist (processName: %q)", app.Name, name)
		} else {
			t.Logf("[ OK ] %s — running (pid %d)", app.Name, pid)
		}
	}

	// 3. Stop all apps.
	t.Log("stopping all apps...")
	out = captureStdout(func() { RunStop(cfg, pm) })
	t.Log(out)

	t.Logf("waiting %s for apps to shut down...", shutdownWait)
	time.Sleep(shutdownWait)

	// 4. Verify each app is stopped.
	t.Log("verifying apps are stopped...")
	for _, app := range cfg.Apps {
		name := app.ProcessName
		if name == "" {
			name = app.Name
		}
		_, running, err := pm.IsRunning(name)
		if err != nil {
			t.Errorf("[FAIL] %s — IsRunning error after stop: %v", app.Name, err)
		} else if running {
			t.Errorf("[FAIL] %s — still running after stop (processName: %q)", app.Name, name)
		} else {
			t.Logf("[ OK ] %s — stopped", app.Name)
		}
	}
}

// isPIDRunning checks tasklist for a specific PID.
func isPIDRunning(pid int) bool {
	filter := fmt.Sprintf("PID eq %d", pid)
	out, err := exec.Command("tasklist", "/FI", filter, "/NH", "/FO", "CSV").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}

// killByPID terminates a process by PID via taskkill.
func killByPID(pid int) error {
	return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
}
