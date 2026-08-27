package gui

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/rickymw/MotorHome/internal/config"
)

// maxConfigBytes caps a config upload. The real file is ~2 KB; anything near
// this is a mistake or a malformed request, and decoding it would be work done
// on behalf of neither.
const maxConfigBytes = 1 << 20 // 1 MiB

type configResponse struct {
	Path   string        `json:"path"`
	Config config.Config `json:"config"`
	// WindowStyles and the field notes are served with the config so the form
	// can build its dropdowns and help text from the same source that validates
	// them, instead of hard-coding a list in JavaScript that drifts when
	// config.Validate changes.
	WindowStyles []string `json:"windowStyles"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadConfigOr500(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, configResponse{
		Path:         s.deps.ConfigPath,
		Config:       cfg,
		WindowStyles: []string{"Normal", "Hidden"},
	})
}

// handlePutConfig validates and writes a whole config document.
//
// It replaces the file wholesale rather than patching fields, because the app
// list is ordered and a patch protocol would need a way to express "move this
// app before that one" that the form does not need — the page always holds the
// complete document it is editing.
func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read request body: "+err.Error())
		return
	}
	if len(body) > maxConfigBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "config document is implausibly large")
		return
	}

	var cfg config.Config
	dec := json.NewDecoder(bytes.NewReader(body))
	// Reject unknown fields: a typo'd key would otherwise be accepted, silently
	// dropped on save, and the user would be left looking at a setting they
	// believe they changed. Better to name the key back at them.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "config is not valid JSON: "+err.Error())
		return
	}
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "config must be a single JSON object")
		return
	}

	// The same Validate the CLI runs on load. Saving a config that `motorhome
	// start` would then refuse to read is the one failure mode this panel must
	// not have: the user would have locked themselves out of the tool through
	// the tool.
	if err := cfg.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.deps.SaveConfig(cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot write config: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, configResponse{
		Path:         s.deps.ConfigPath,
		Config:       cfg,
		WindowStyles: []string{"Normal", "Hidden"},
	})
}
