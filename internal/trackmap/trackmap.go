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
// EntryPct and ExitPct are lap distance fractions (0.0–1.0) at the geometric
// track boundaries (where the road bends). EntryM and ExitM are the same in metres.
//
// BrakeEntryPct is the average lap-distance fraction at which drivers begin
// braking for this corner/chicane. It is computed from telemetry and stored
// so that comparison laps use a consistent shared boundary. Zero means not yet
// computed (treat as EntryPct). Only set for corners and chicanes; zero for straights.
type Segment struct {
	Name          string      `json:"name"`
	Kind          SegmentKind `json:"kind"`
	EntryPct      float32     `json:"entryPct"`
	ExitPct       float32     `json:"exitPct"`
	EntryM        float32     `json:"entryM"`
	ExitM         float32     `json:"exitM"`
	BrakeEntryPct float32     `json:"brakeEntryPct,omitempty"`
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
	TrackLengthM float64   `json:"trackLengthM"`
	Source       string    `json:"source"`              // "auto" or "manual"
	DetectedFrom string    `json:"detectedFrom"`        // date string (YYYY-MM-DD)
	GeoMethod    string    `json:"geoMethod,omitempty"` // "lataccel" or "latlon"; empty = "lataccel" for backward compat
	LapsUsed     int       `json:"lapsUsed"`
	SessionsUsed int       `json:"sessionsUsed"`
	SeenSessions []string  `json:"seenSessions"` // RFC3339 session start dates already counted
	Segments     []Segment `json:"segments"`
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

// maxSeenSessions caps the seenSessions list to prevent unbounded growth.
// Only the most recent entries are retained since the list is used only for
// deduplication of sessions already counted in LapsUsed/SessionsUsed.
const maxSeenSessions = 50

// AddSession records sessionID as seen. It is a no-op if sessionID is already present.
// The list is capped at maxSeenSessions entries (oldest are dropped first).
func (tm *TrackMap) AddSession(sessionID string) {
	if tm.HasSession(sessionID) {
		return
	}
	tm.SeenSessions = append(tm.SeenSessions, sessionID)
	if len(tm.SeenSessions) > maxSeenSessions {
		tm.SeenSessions = tm.SeenSessions[len(tm.SeenSessions)-maxSeenSessions:]
	}
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

// confidenceRank returns a numeric rank for ordering (higher = more confident).
func confidenceRank(c GeometryConfidence) int {
	switch c {
	case ConfHigh:
		return 2
	case ConfModerate:
		return 1
	default:
		return 0
	}
}

// MatchConfidence converts a segment match score (0.0–1.0) to a confidence level.
//
//   - score ≥ 0.93 → high
//   - score ≥ 0.80 → moderate
//   - score  < 0.80 → low
func MatchConfidence(score float32) GeometryConfidence {
	switch {
	case score >= 0.93:
		return ConfHigh
	case score >= 0.80:
		return ConfModerate
	default:
		return ConfLow
	}
}

// EffectiveConfidence returns the lower of the geometry confidence (derived from
// laps used) and the match confidence (derived from how well this session's
// telemetry fits the stored segments). This prevents a well-established map
// from displaying as "high" when the current session only matches moderately.
func (tm *TrackMap) EffectiveConfidence(matchScore float32) GeometryConfidence {
	geom := tm.Confidence()
	match := MatchConfidence(matchScore)
	if confidenceRank(match) < confidenceRank(geom) {
		return match
	}
	return geom
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
