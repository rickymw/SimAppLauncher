// Package trackmap provides types and file I/O for the persistent track segment store.
// Segments are detected from lateral acceleration telemetry and cached as JSON
// so that detection only runs once per track.
package trackmap

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// SegmentKind classifies a track segment.
type SegmentKind string

const (
	KindStraight SegmentKind = "straight"
	KindCorner   SegmentKind = "corner"
	KindChicane  SegmentKind = "chicane"
)

// Segment describes one straight, corner, or chicane on the track.
// EntryPct and ExitPct are lap distance fractions (0.0–1.0).
// EntryM and ExitM are the corresponding distances in metres.
type Segment struct {
	Name     string      `json:"name"`
	Kind     SegmentKind `json:"kind"`
	EntryPct float32     `json:"entryPct"`
	ExitPct  float32     `json:"exitPct"`
	EntryM   float32     `json:"entryM"`
	ExitM    float32     `json:"exitM"`
}

// GeometryConfidence describes how well-established the stored track map is,
// based on the number of laps used to build it.
type GeometryConfidence string

const (
	ConfLow      GeometryConfidence = "low"
	ConfModerate GeometryConfidence = "moderate"
	ConfHigh     GeometryConfidence = "high"
)

// TrackMap holds the full segment map for one track configuration.
type TrackMap struct {
	TrackLengthM float64    `json:"trackLengthM"`
	Source       string     `json:"source"`       // "auto" or "manual"
	DetectedFrom string     `json:"detectedFrom"` // date string (YYYY-MM-DD)
	LapsUsed     int        `json:"lapsUsed"`
	SessionsUsed int        `json:"sessionsUsed"`
	SeenSessions []string   `json:"seenSessions"` // RFC3339 session start dates already counted
	Segments     []Segment  `json:"segments"`
}

// HasSession reports whether sessionID has already been counted.
func (tm *TrackMap) HasSession(sessionID string) bool {
	for _, s := range tm.SeenSessions {
		if s == sessionID {
			return true
		}
	}
	return false
}

// AddSession records sessionID as seen. It is a no-op if sessionID is already present.
func (tm *TrackMap) AddSession(sessionID string) {
	if tm.HasSession(sessionID) {
		return
	}
	tm.SeenSessions = append(tm.SeenSessions, sessionID)
}

// Confidence returns a GeometryConfidence level based on the number of laps
// used to build this track map.
//
//   - < 3 laps  → low
//   - 3–10 laps → moderate
//   - > 10 laps → high
func (tm *TrackMap) Confidence() GeometryConfidence {
	switch {
	case tm.LapsUsed > 10:
		return ConfHigh
	case tm.LapsUsed >= 3:
		return ConfModerate
	default:
		return ConfLow
	}
}

// TrackMapFile is the top-level store keyed by TrackDisplayName.
type TrackMapFile map[string]*TrackMap

// Load reads a TrackMapFile from path.
// Returns an empty TrackMapFile (not an error) if the file does not exist.
func Load(path string) (TrackMapFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TrackMapFile{}, nil
		}
		return nil, err
	}
	var tmf TrackMapFile
	if err := json.Unmarshal(data, &tmf); err != nil {
		return nil, err
	}
	return tmf, nil
}

// Save writes tmf to path as indented JSON.
func Save(path string, tmf TrackMapFile) error {
	data, err := json.MarshalIndent(tmf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Today returns the current date as a YYYY-MM-DD string.
func Today() string {
	return time.Now().Format("2006-01-02")
}
