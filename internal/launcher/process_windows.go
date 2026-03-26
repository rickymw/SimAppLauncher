//go:build windows

package launcher

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/rickymw/SimAppLauncher/internal/config"
)

type windowsProcessManager struct{}

// NewProcessManager returns the Windows implementation of ProcessManager.
func NewProcessManager() ProcessManager {
	return &windowsProcessManager{}
}

func (w *windowsProcessManager) Spawn(app config.App) SpawnResult {
	if app.Elevate {
		return spawnElevated(app)
	}

	args := strings.Fields(app.Args)
	cmd := exec.Command(app.Path, args...)

	// Hidden: suppress window entirely via HideWindow.
	// Minimized: requires golang.org/x/sys/windows for StartupInfo; treated as Normal for now.
	hideWindow := strings.ToLower(app.WindowStyle) == "hidden"
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: hideWindow,
	}

	if err := cmd.Start(); err != nil {
		return SpawnResult{Err: err}
	}
	return SpawnResult{PID: cmd.Process.Pid}
}

func (w *windowsProcessManager) IsRunning(processName string) (pid int, running bool) {
	filter := fmt.Sprintf("IMAGENAME eq %s.exe", processName)
	out, err := exec.Command("tasklist", "/FI", filter, "/NH", "/FO", "CSV").Output()
	if err != nil {
		return 0, false
	}

	output := string(out)
	lowerName := strings.ToLower(processName + ".exe")
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "\""+lowerName+"\"") {
			// CSV line: "name.exe","pid","Console","1","10,000 K"
			fields := strings.Split(strings.TrimSpace(line), ",")
			if len(fields) >= 2 {
				pidStr := strings.Trim(fields[1], "\"")
				if p, err := strconv.Atoi(pidStr); err == nil {
					return p, true
				}
			}
		}
	}
	return 0, false
}

func (w *windowsProcessManager) Kill(processName string) error {
	return exec.Command("taskkill", "/F", "/IM", processName+".exe").Run()
}
