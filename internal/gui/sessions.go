package gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// analyzeTimeout bounds one analysis run. A long session with several flying
// laps takes a couple of seconds; anything past this has hung rather than
// slowed, and the server must not hold the request forever.
const analyzeTimeout = 3 * time.Minute

// maxSessions caps the .ibt listing. The directory accumulates every session
// ever recorded and the picker only ever needs the recent end of it.
const maxSessions = 200

type sessionFile struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	// Index is the 1-based position in most-recent-first order, which is what
	// `analyze N` on the command line takes. Showing it lets the page and the
	// CLI refer to the same session the same way.
	Index int `json:"index"`
}

type sessionsResponse struct {
	Dir      string        `json:"dir"`
	Sessions []sessionFile `json:"sessions"`
	// Truncated says the listing was cut at maxSessions, so the page can say so
	// rather than implying the directory holds only what is shown.
	Truncated bool `json:"truncated,omitempty"`
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.loadConfigOr500(w)
	if !ok {
		return
	}
	if strings.TrimSpace(cfg.IbtDir) == "" {
		writeErr(w, http.StatusBadRequest, "no ibtDir is set — add one in Settings before browsing sessions")
		return
	}

	entries, err := os.ReadDir(cfg.IbtDir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read ibtDir: "+err.Error())
		return
	}

	// modTimes is kept alongside rather than sorting on the formatted string:
	// RFC3339 only sorts lexicographically while every timestamp shares an
	// offset, and a directory copied across a DST boundary breaks that.
	type dated struct {
		file    sessionFile
		modTime time.Time
	}
	found := make([]dated, 0, len(entries))
	for _, e := range entries {
		// Case-insensitive, matching nthLatestIbtFile: Windows filesystems are,
		// so a "SESSION.IBT" must not be invisible here while the CLI finds it.
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".ibt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, dated{
			file: sessionFile{
				Name:     e.Name(),
				Path:     filepath.Join(cfg.IbtDir, e.Name()),
				Size:     info.Size(),
				Modified: info.ModTime().Format(time.RFC3339),
			},
			modTime: info.ModTime(),
		})
	}

	// Most recent first — the same order the CLI's numeric argument counts in.
	sort.Slice(found, func(i, j int) bool { return found[i].modTime.After(found[j].modTime) })

	files := make([]sessionFile, len(found))
	for i, d := range found {
		files[i] = d.file
	}

	resp := sessionsResponse{Dir: cfg.IbtDir}
	if len(files) > maxSessions {
		files = files[:maxSessions]
		resp.Truncated = true
	}
	for i := range files {
		files[i].Index = i + 1
	}
	resp.Sessions = files

	writeJSON(w, http.StatusOK, resp)
}

// handleAnalyze runs the analysis and hands back the -json document untouched.
//
// It re-execs this binary rather than calling RunAnalyze in-process, which is
// the opposite of what the coach subcommand does, for two reasons that only
// apply to a server. First, every error path in the analyze pipeline ends in
// analyzeDie -> os.Exit(1): in the CLI that is a clean exit, but here a mistyped
// lap number would take the whole interface down mid-session. Second, the
// pipeline writes package-level globals (analyzeOut, invokedAs) and swaps
// os.Stdout, none of which survives two requests arriving at once.
//
// Drift is not a risk despite the separate path, because it is not a separate
// path: it is this same executable running the same subcommand.
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.deps.RunSubcommand == nil {
		writeErr(w, http.StatusNotImplemented, "analysis is not available in this build")
		return
	}

	args, err := analyzeArgs(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	s.analyzeMu.Lock()
	defer s.analyzeMu.Unlock()

	out, err := s.deps.RunSubcommand(analyzeTimeout, args...)
	if err != nil {
		// The subcommand writes its one-line reason to stderr, which
		// RunSubcommand folds into out. That message is what the user needs
		// ("analyze: lap 99 not found"), so pass it on rather than the generic
		// exit-status error.
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		writeErr(w, http.StatusBadRequest, msg)
		return
	}

	doc, ok := extractJSONDocument(out)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "analysis produced no JSON document")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(doc)
}

// analyzeArgs turns the query string into an argv for `analyze -json`.
//
// The file is passed through as-is when given: it comes from the sessions
// listing, which only ever yields paths inside ibtDir, and the analyze
// subcommand is the thing that decides whether a path is usable. Every other
// parameter is parsed here so a bad value is a 400 with a readable reason
// rather than an exit code from a subprocess.
func analyzeArgs(r *http.Request) ([]string, error) {
	args := []string{"analyze", "-json"}
	q := r.URL.Query()

	if lap := strings.TrimSpace(q.Get("lap")); lap != "" {
		// "pb" and a positive integer are the two forms the subcommand takes.
		if !strings.EqualFold(lap, "pb") {
			n, err := strconv.Atoi(lap)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("lap must be a positive number or %q, got %q", "pb", lap)
			}
			lap = strconv.Itoa(n)
		}
		args = append(args, "-lap", lap)
	}

	if v := strings.TrimSpace(q.Get("fuelLaps")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("fuelLaps must be a positive number, got %q", v)
		}
		args = append(args, "-fuel-laps", strconv.Itoa(n))
	}

	if v := strings.TrimSpace(q.Get("noteLag")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 {
			return nil, fmt.Errorf("noteLag must be a non-negative number of seconds, got %q", v)
		}
		args = append(args, "-note-lag", strconv.FormatFloat(f, 'f', -1, 64))
	}

	if q.Get("updateMap") == "true" {
		args = append(args, "-update-map")
	}

	// -trace needs a track map to resolve names against, so an empty value is
	// simply omitted rather than passed through as an empty flag the subcommand
	// would reject.
	if seg := strings.TrimSpace(q.Get("trace")); seg != "" {
		args = append(args, "-trace", seg)
		if v := strings.TrimSpace(q.Get("hz")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 60 {
				return nil, fmt.Errorf("hz must be between 1 and 60, got %q", v)
			}
			args = append(args, "-hz", strconv.Itoa(n))
		}
	}

	if file := strings.TrimSpace(q.Get("file")); file != "" {
		// A leading dash would be read as a flag by the subcommand's flag set.
		if strings.HasPrefix(file, "-") {
			return nil, fmt.Errorf("file must not start with a dash, got %q", file)
		}
		args = append(args, file)
	}

	return args, nil
}

// extractJSONDocument pulls the analyze document out of the subcommand's
// combined output, which has prose on both sides of it.
//
// Ahead of the document are any warnings analyze wrote to stderr — a new PB
// with no track map, an unreadable notes file. Behind it is "(copied to
// clipboard)", because main.go wraps every analyze run in the clipboard tee
// regardless of -json. Trimming to the first '{' handles the first of those and
// not the second, so this decodes exactly one JSON value and slices at the
// offset the decoder stopped at, which handles both.
// It tries each '{' in turn rather than only the first, so a warning that
// happens to contain a brace does not take the whole response down — the
// document is whichever brace begins something that actually parses.
func extractJSONDocument(b []byte) ([]byte, bool) {
	for offset := 0; ; {
		i := bytes.IndexByte(b[offset:], '{')
		if i < 0 {
			return nil, false
		}
		rest := b[offset+i:]

		dec := json.NewDecoder(bytes.NewReader(rest))
		var doc json.RawMessage
		if err := dec.Decode(&doc); err == nil {
			// InputOffset is where the decoder stopped, i.e. the end of the
			// document. Slicing rather than re-encoding doc keeps the
			// subcommand's own formatting and avoids a round trip through
			// map[string]any.
			return rest[:dec.InputOffset()], true
		}
		offset += i + 1
	}
}
