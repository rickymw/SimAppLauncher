package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// LiveSnapshot is one frame of the live panel: where the player is and who is
// immediately around them.
//
// It is this package's own shape rather than iracing.LiveData because that type
// is Windows-only and carries the raw per-CarIdx arrays. The Windows shim
// reduces those to the same two gaps the `live` subcommand prints, using the
// same helpers, so the browser and the terminal cannot report different gaps
// for the same moment.
type LiveSnapshot struct {
	Connected bool `json:"connected"`
	// Message is the plain-language reason there is nothing to show. Detail is
	// the underlying diagnostic behind it, kept separate because they are read
	// by different people at different moments: a panel glanced at mid-session
	// wants "iRacing is not running", and only someone debugging wants
	// "OpenFileMappingW: The system cannot find the file specified".
	//
	// The `live` subcommand prints the diagnostic as its whole message, which
	// is right for a command whose -raw mode exists to troubleshoot this. A
	// dashboard is not that.
	Message string `json:"message,omitempty"`
	Detail  string `json:"detail,omitempty"`

	Track string `json:"track,omitempty"`
	Car   string `json:"car,omitempty"`

	Position      int     `json:"position,omitempty"`
	FieldSize     int     `json:"fieldSize,omitempty"`
	ClassPosition int     `json:"classPosition,omitempty"`
	ClassSize     int     `json:"classSize,omitempty"`
	Lap           int     `json:"lap,omitempty"`
	LapDistPct    float32 `json:"lapDistPct"`

	Ahead  *LiveGap `json:"ahead,omitempty"`
	Behind *LiveGap `json:"behind,omitempty"`
}

// LiveGap describes one neighbouring car. A nil *LiveGap means there is nobody
// in that direction — a solo practice session, which is a normal state and not
// an error.
type LiveGap struct {
	DriverName  string  `json:"driverName,omitempty"`
	CarNumber   string  `json:"carNumber,omitempty"`
	TimeSeconds float32 `json:"timeSeconds"`
	LapsDelta   int     `json:"lapsDelta,omitempty"`
}

// liveStreamMaxHz mirrors the `live` subcommand's clamp. Above 60 Hz there is
// nothing new to read — iRacing publishes at 60 — and each frame is a browser
// repaint.
const (
	liveStreamMinHz     = 1
	liveStreamMaxHz     = 60
	liveStreamDefaultHz = 5
)

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if s.deps.Live == nil {
		unsupported(w, "live telemetry")
		return
	}
	writeJSON(w, http.StatusOK, s.deps.Live.Snapshot())
}

// handleLiveStream pushes a snapshot per tick as server-sent events.
//
// SSE rather than a WebSocket because net/http speaks it with no framing code
// and no dependency: the data only ever flows one way, and the browser's
// EventSource reconnects on its own. A poll loop from the page would work too,
// but at 5–60 Hz it would mean a request per frame.
func (s *Server) handleLiveStream(w http.ResponseWriter, r *http.Request) {
	if s.deps.Live == nil {
		unsupported(w, "live telemetry")
		return
	}

	hz := liveStreamDefaultHz
	if v := r.URL.Query().Get("hz"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "hz must be a number, got "+strconv.Quote(v))
			return
		}
		hz = min(max(n, liveStreamMinHz), liveStreamMaxHz)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "this server cannot stream")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Nothing here is proxied in normal use, but a buffering proxy would hold
	// the whole stream until it ended, which for this endpoint is never.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	tick := time.NewTicker(time.Second / time.Duration(hz))
	defer tick.Stop()

	// The request context is the only stop signal: the page closes the
	// EventSource when the user leaves the panel, and the handler must return
	// then rather than reading shared memory forever.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			payload, err := json.Marshal(s.deps.Live.Snapshot())
			if err != nil {
				// A snapshot that cannot encode is a bug in the shim, not a
				// transient condition; ending the stream surfaces it rather
				// than silently dropping frames forever.
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
