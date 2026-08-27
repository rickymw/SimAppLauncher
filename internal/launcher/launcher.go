package launcher

import (
	"fmt"

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

// RunStart launches the apps and prints what happened. The work is in Start;
// this is only the terminal rendering of it (see ops.go).
func RunStart(cfg config.Config, pm ProcessManager) {
	results := Start(cfg, pm)
	for _, r := range results {
		switch r.Outcome {
		case OutcomeAlreadyRunning:
			PrintAlreadyRunning(r.Name, r.PID)
		case OutcomeLaunched:
			PrintLaunched(r.Name, r.PID)
		default:
			PrintFailed(r.Name, r.Err)
		}
	}
	fmt.Printf("\nDone. %d/%d apps running.\n", CountRunning(results), len(cfg.Apps))
}

// RunStop kills the apps and prints what happened.
func RunStop(cfg config.Config, pm ProcessManager) {
	for _, r := range Stop(cfg, pm) {
		if r.Outcome == OutcomeFailed {
			PrintFailed(r.Name, r.Err)
			continue
		}
		PrintClosed(r.Name)
	}
}

// RunStatus prints the running/stopped state of every app.
func RunStatus(cfg config.Config, pm ProcessManager) {
	for _, r := range Status(cfg, pm) {
		if r.Outcome == OutcomeFailed {
			PrintStatusError(r.Name, r.Err)
			continue
		}
		PrintStatus(r.Name, r.Running(), r.PID)
	}
}
