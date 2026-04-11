// Package trackmap provides types and file I/O for the persistent track segment store.
// Segments are detected from lateral acceleration telemetry and cached as JSON
// so that detection only runs once per track.
package trackmap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
// Brake onset positions are driver/car-specific and are stored separately in
// brakeentry.json (see internal/pb.BrakeEntryFile), not in the geometry map.
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
	TrackLengthM float64   `json:"trackLengthM"`
	Source       string    `json:"source"`              // "auto" or "manual"
	DetectedFrom string    `json:"detectedFrom"`        // date string (YYYY-MM-DD)
	GeoMethod    string    `json:"geoMethod,omitempty"` // "latlon"; empty = "latlon" for backward compat
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

// legacySegment is used only during JSON loading to absorb the old
// brakeEntryPct field that was removed from Segment. Go's JSON decoder
// ignores unknown fields by default, but this field was previously known —
// the shim prevents any decode error if old JSON is present.
type legacySegment struct {
	Name          string      `json:"name"`
	Kind          SegmentKind `json:"kind"`
	EntryPct      float32     `json:"entryPct"`
	ExitPct       float32     `json:"exitPct"`
	EntryM        float32     `json:"entryM"`
	ExitM         float32     `json:"exitM"`
	BrakeEntryPct float32     `json:"brakeEntryPct,omitempty"` // legacy — ignored
}

// legacyTrackMap mirrors TrackMap but uses legacySegment for deserialization.
type legacyTrackMap struct {
	TrackLengthM float64          `json:"trackLengthM"`
	Source       string           `json:"source"`
	DetectedFrom string           `json:"detectedFrom"`
	GeoMethod    string           `json:"geoMethod,omitempty"`
	LapsUsed     int              `json:"lapsUsed"`
	SessionsUsed int              `json:"sessionsUsed"`
	SeenSessions []string         `json:"seenSessions"`
	Segments     []legacySegment  `json:"segments"`
}

// Load reads a TrackMapFile from path.
// Returns an empty TrackMapFile (not an error) if the file does not exist.
// Old trackmap.json files that contain brakeEntryPct on segments are handled
// gracefully — those values are silently dropped during load.
func Load(path string) (TrackMapFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TrackMapFile{}, nil
		}
		return nil, err
	}
	// Deserialize via legacy types to absorb removed fields.
	var raw map[string]*legacyTrackMap
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	tmf := make(TrackMapFile, len(raw))
	for track, ltm := range raw {
		if ltm == nil {
			continue
		}
		segs := make([]Segment, len(ltm.Segments))
		for i, ls := range ltm.Segments {
			segs[i] = Segment{
				Name:     ls.Name,
				Kind:     ls.Kind,
				EntryPct: ls.EntryPct,
				ExitPct:  ls.ExitPct,
				EntryM:   ls.EntryM,
				ExitM:    ls.ExitM,
			}
		}
		tmf[track] = &TrackMap{
			TrackLengthM: ltm.TrackLengthM,
			Source:       ltm.Source,
			DetectedFrom: ltm.DetectedFrom,
			GeoMethod:    ltm.GeoMethod,
			LapsUsed:     ltm.LapsUsed,
			SessionsUsed: ltm.SessionsUsed,
			SeenSessions: ltm.SeenSessions,
			Segments:     segs,
		}
	}
	return tmf, nil
}

// Save writes tmf to path as indented JSON.
// Uses write-to-temp-then-rename to avoid corruption if interrupted mid-write.
func Save(path string, tmf TrackMapFile) error {
	data, err := json.MarshalIndent(tmf, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// writeFileAtomic writes data to a temp file in the same directory as path,
// then renames it over path. This ensures the file is never left in a
// partially-written state.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Today returns the current date as a YYYY-MM-DD string.
func Today() string {
	return time.Now().Format("2006-01-02")
}
