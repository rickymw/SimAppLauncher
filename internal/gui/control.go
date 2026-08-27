package gui

import (
	"net/http"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/launcher"
)

// controlResponse is what the rig control panel gets back from status, start
// and stop alike. One shape for all three because the panel redraws the same
// table from each of them — a start that reports what is now running saves the
// page a follow-up status poll.
type controlResponse struct {
	Apps    []launcher.AppResult `json:"apps"`
	Running int                  `json:"running"`
	Total   int                  `json:"total"`
}

func controlResponseOf(results []launcher.AppResult) controlResponse {
	return controlResponse{
		Apps:    results,
		Running: launcher.CountRunning(results),
		Total:   len(results),
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadConfigOr500(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, controlResponseOf(launcher.Status(cfg, s.deps.NewProcessManager())))
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadConfigOr500(w)
	if !ok {
		return
	}
	// launcher.Start honours each app's delayMs, so this request takes as long
	// as the configured delays add up to. That is the same wait the CLI has and
	// the same wait the rig actually needs; the page shows a spinner rather
	// than the server cutting the sequence short.
	writeJSON(w, http.StatusOK, controlResponseOf(launcher.Start(cfg, s.deps.NewProcessManager())))
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadConfigOr500(w)
	if !ok {
		return
	}
	pm := s.deps.NewProcessManager()

	// A kill that returns without error says taskkill was accepted, not that
	// the process is gone — SimHub restarts itself, and an app with several
	// instances only loses the ones matching the image name. Re-checking is
	// what makes the panel show the rig's actual state rather than the state
	// the stop attempt implied, so any failure is merged into a fresh status
	// pass instead of being reported on its own.
	failures := make(map[string]string)
	for _, r := range launcher.Stop(cfg, pm) {
		if r.Outcome == launcher.OutcomeFailed {
			failures[r.Name] = r.Err
		}
	}

	results := launcher.Status(cfg, pm)
	for i := range results {
		if msg, bad := failures[results[i].Name]; bad {
			results[i].Outcome = launcher.OutcomeFailed
			results[i].Err = msg
		}
	}
	writeJSON(w, http.StatusOK, controlResponseOf(results))
}

// loadConfigOr500 reads the config, reporting a failure to the client and
// returning false if it cannot. Every handler that needs the app list calls it
// rather than caching a config at boot, because the settings panel can rewrite
// the file while the server is running.
func (s *Server) loadConfigOr500(w http.ResponseWriter) (config.Config, bool) {
	cfg, err := s.deps.LoadConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot read config: "+err.Error())
		return config.Config{}, false
	}
	return cfg, true
}
