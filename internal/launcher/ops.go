package launcher

import (
	"time"

	"github.com/rickymw/MotorHome/internal/config"
)

// This file holds the computed half of start/stop/status. RunStart, RunStop and
// RunStatus in launcher.go are thin printers over these, the same relationship
// analyze_json.go has to the ASCII tables: one set of values, two renderers.
// The GUI needs the values as data rather than as formatted lines, and a second
// implementation of "is this app running, and if not, launch it" would be free
// to drift from what the CLI reports.

// Outcome is what happened to one app during a start, stop or status pass.
type Outcome string

const (
	// OutcomeLaunched means the app was not running and was started.
	OutcomeLaunched Outcome = "launched"
	// OutcomeAlreadyRunning means the app was found running, so Start left it alone.
	OutcomeAlreadyRunning Outcome = "already-running"
	// OutcomeClosed means Kill returned without error.
	OutcomeClosed Outcome = "closed"
	// OutcomeRunning / OutcomeStopped are the two states Status reports.
	OutcomeRunning Outcome = "running"
	OutcomeStopped Outcome = "stopped"
	// OutcomeFailed means the operation errored; Err carries the reason.
	OutcomeFailed Outcome = "failed"
)

// AppResult is one app's row in a start, stop or status pass.
//
// Err is a string rather than an error because these are marshalled straight to
// JSON by the GUI, and an error value would encode as {}.
type AppResult struct {
	Name    string  `json:"name"`
	Process string  `json:"process"`
	Outcome Outcome `json:"outcome"`
	PID     int     `json:"pid,omitempty"`
	Err     string  `json:"error,omitempty"`
}

// Running reports whether this result describes an app that is up. Both a
// freshly launched app and one that was already running count.
func (r AppResult) Running() bool {
	return r.Outcome == OutcomeLaunched || r.Outcome == OutcomeAlreadyRunning || r.Outcome == OutcomeRunning
}

// processName is the name tasklist/taskkill are given for an app: the explicit
// processName when set, otherwise the display name.
func processName(app config.App) string {
	if app.ProcessName != "" {
		return app.ProcessName
	}
	return app.Name
}

// Status checks every app in cfg and reports whether it is running.
func Status(cfg config.Config, pm ProcessManager) []AppResult {
	results := make([]AppResult, 0, len(cfg.Apps))
	for _, app := range cfg.Apps {
		name := processName(app)
		pid, running, err := pm.IsRunning(name)
		switch {
		case err != nil:
			results = append(results, AppResult{Name: app.Name, Process: name, Outcome: OutcomeFailed, Err: err.Error()})
		case running:
			results = append(results, AppResult{Name: app.Name, Process: name, Outcome: OutcomeRunning, PID: pid})
		default:
			results = append(results, AppResult{Name: app.Name, Process: name, Outcome: OutcomeStopped})
		}
	}
	return results
}

// Start launches every app in cfg that is not already running, honouring each
// app's delayMs before moving to the next.
//
// The per-app delay is deliberately kept here rather than hoisted into the
// caller: it is part of what "start the rig" means — some apps need the one
// before them to be up before they will attach — so a caller that skipped it
// would be starting a different sequence, not the same one faster.
func Start(cfg config.Config, pm ProcessManager) []AppResult {
	results := make([]AppResult, 0, len(cfg.Apps))
	for _, app := range cfg.Apps {
		name := processName(app)
		pid, running, err := pm.IsRunning(name)
		if err != nil {
			results = append(results, AppResult{
				Name: app.Name, Process: name, Outcome: OutcomeFailed,
				Err: "status check failed: " + err.Error(),
			})
			continue
		}
		if running {
			results = append(results, AppResult{Name: app.Name, Process: name, Outcome: OutcomeAlreadyRunning, PID: pid})
			continue
		}

		res := pm.Spawn(app)
		if res.Err != nil {
			results = append(results, AppResult{Name: app.Name, Process: name, Outcome: OutcomeFailed, Err: res.Err.Error()})
		} else {
			results = append(results, AppResult{Name: app.Name, Process: name, Outcome: OutcomeLaunched, PID: res.PID})
		}
		if app.DelayMs > 0 {
			time.Sleep(time.Duration(app.DelayMs) * time.Millisecond)
		}
	}
	return results
}

// Stop kills every app in cfg.
func Stop(cfg config.Config, pm ProcessManager) []AppResult {
	results := make([]AppResult, 0, len(cfg.Apps))
	for _, app := range cfg.Apps {
		name := processName(app)
		if err := pm.Kill(name); err != nil {
			results = append(results, AppResult{Name: app.Name, Process: name, Outcome: OutcomeFailed, Err: err.Error()})
			continue
		}
		results = append(results, AppResult{Name: app.Name, Process: name, Outcome: OutcomeClosed})
	}
	return results
}

// CountRunning returns how many results describe an app that is up.
func CountRunning(results []AppResult) int {
	n := 0
	for _, r := range results {
		if r.Running() {
			n++
		}
	}
	return n
}
