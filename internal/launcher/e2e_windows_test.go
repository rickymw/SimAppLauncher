//go:build e2e

package launcher

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/SimAppLauncher/internal/config"
)

// TestE2E_SpawnIsRunningKill exercises the full Windows process lifecycle:
// Spawn → IsRunning → Kill → confirm stopped.
//
// Uses notepad.exe as a benign, always-available test process.
// Idempotent: kills only the specific PID it spawned, so any pre-existing
// notepad windows are left untouched.
//
// Run with: go test -tags e2e -v ./internal/launcher/
func TestE2E_SpawnIsRunningKill(t *testing.T) {
	pm := NewProcessManager()

	app := config.App{
		Name:        "notepad (e2e test)",
		Path:        `C:\Windows\System32\notepad.exe`,
		WindowStyle: "Hidden",
		ProcessName: "notepad",
	}

	// 1. Spawn
	result := pm.Spawn(app)
	if result.Err != nil {
		t.Fatalf("Spawn failed: %v", result.Err)
	}
	if result.PID == 0 {
		t.Fatal("Spawn returned PID 0")
	}
	t.Logf("spawned notepad.exe with pid %d", result.PID)

	// Ensure we always clean up, even on test failure.
	t.Cleanup(func() {
		killByPID(result.PID) // no-op if already dead
	})

	// Give the process a moment to appear in the process list.
	time.Sleep(200 * time.Millisecond)

	// 2. IsRunning by PID — expect running
	if !isPIDRunning(result.PID) {
		t.Fatalf("process %d not found in tasklist after Spawn", result.PID)
	}
	t.Logf("confirmed pid %d is running", result.PID)

	// 3. Kill by PID (leaves any other notepad windows untouched)
	if err := killByPID(result.PID); err != nil {
		t.Fatalf("kill by PID failed: %v", err)
	}
	t.Logf("killed pid %d", result.PID)

	time.Sleep(200 * time.Millisecond)

	// 4. IsRunning by PID — expect stopped
	if isPIDRunning(result.PID) {
		t.Fatalf("process %d still running after kill", result.PID)
	}
	t.Log("process confirmed stopped")
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
