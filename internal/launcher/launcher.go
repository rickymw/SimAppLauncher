package launcher

import (
	"fmt"
	"time"

	"github.com/rickymw/MotorHome/internal/config"
)

// SpawnResult holds the result of launching a single process.
type SpawnResult struct {
	PID int
	Err error
}

// ProcessManager abstracts OS-level process operations so they can be mocked in tests.
type ProcessManager interface {
	Spawn(app config.App) SpawnResult
	IsRunning(processName string) (pid int, running bool, err error)
	Kill(processName string) error
}

func RunStart(cfg config.Config, pm ProcessManager) {
	launched := 0
	alreadyRunning := 0
	for _, app := range cfg.Apps {
		name := app.ProcessName
		if name == "" {
			name = app.Name
		}
		pid, running, err := pm.IsRunning(name)
		if err != nil {
			PrintFailed(app.Name, "status check failed: "+err.Error())
			continue
		}
		if running {
			PrintAlreadyRunning(app.Name, pid)
			alreadyRunning++
			continue
		}
		result := pm.Spawn(app)
		if result.Err != nil {
			PrintFailed(app.Name, result.Err.Error())
		} else {
			PrintLaunched(app.Name, result.PID)
			launched++
		}
		if app.DelayMs > 0 {
			time.Sleep(time.Duration(app.DelayMs) * time.Millisecond)
		}
	}
	fmt.Printf("\nDone. %d/%d apps running.\n", launched+alreadyRunning, len(cfg.Apps))
}

func RunStop(cfg config.Config, pm ProcessManager) {
	for _, app := range cfg.Apps {
		name := app.ProcessName
		if name == "" {
			name = app.Name
		}
		err := pm.Kill(name)
		if err != nil {
			PrintFailed(app.Name, err.Error())
		} else {
			PrintClosed(app.Name)
		}
	}
}

func RunStatus(cfg config.Config, pm ProcessManager) {
	for _, app := range cfg.Apps {
		name := app.ProcessName
		if name == "" {
			name = app.Name
		}
		pid, running, err := pm.IsRunning(name)
		if err != nil {
			PrintStatusError(app.Name, err.Error())
			continue
		}
		PrintStatus(app.Name, running, pid)
	}
}
