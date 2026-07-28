// Package pb tracks personal-best lap times per car/track combination.
// Results are persisted to a JSON file (pb.json) next to the binary.
package pb

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PersonalBest holds the fastest recorded lap for a single car/track combo,
// plus accumulated per-corner brake onset positions for this car/track.
type PersonalBest struct {
	LapTime          float32       `json:"lapTime"`          // seconds
	LapTimeFormatted string        `json:"lapTimeFormatted"` // e.g. "2:11.367"
	Date             string        `json:"date"`             // "YYYY-MM-DD"
	Weather          string        `json:"weather"`          // e.g. "Partly Cloudy, 27°C"
	Car              string        `json:"car"`
	Track            string        `json:"track"`
	BrakeEntries     BrakeEntryMap `json:"brakeEntries,omitempty"` // segment name → brake onset
	Phases           []PBPhase     `json:"phases,omitempty"`       // phase data from PB lap
	Setup            string        `json:"setup,omitempty"`        // raw "CarSetup:" YAML block from the PB session
}

// File is the top-level structure stored in pb.json: a map from Key → PersonalBest.
type File map[string]*PersonalBest

// Key returns the map key for a car/track combination.
func Key(car, track string) string {
	return car + "|" + track
}

// Load reads pb.json from path. Returns an empty File if the file does not exist.
func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return File{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f == nil {
		f = File{}
	}
	return f, nil
}

// Save writes pbf to path as indented JSON.
// Uses write-to-temp-then-rename to avoid corruption if interrupted mid-write.
func Save(path string, pbf File) error {
	b, err := json.MarshalIndent(pbf, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b)
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

// Update checks whether lapTime beats the stored PB for the given car/track.
// If so (or if no PB exists yet), it updates pbf in-place and returns true.
// An entry with LapTime == 0 is treated as "no PB yet" — those are created as
// side effects of BrakeEntrySet and must not block a real PB from being saved.
// date should be "YYYY-MM-DD"; weather is a human-readable string or "".
func Update(pbf File, car, track string, lapTime float32, formatted, date, weather string) bool {
	key := Key(car, track)
	existing, ok := pbf[key]
	if ok && existing.LapTime > 0 && existing.LapTime <= lapTime {
		return false
	}
	// Preserve accumulated brake entries when replacing a PB — they are
	// session-independent and must not be discarded on a new personal best.
	var brakeEntries BrakeEntryMap
	if ok {
		brakeEntries = existing.BrakeEntries
	}
	pbf[key] = &PersonalBest{
		LapTime:          lapTime,
		LapTimeFormatted: formatted,
		Date:             date,
		Weather:          weather,
		Car:              car,
		Track:            track,
		BrakeEntries:     brakeEntries,
	}
	return true
}

// SetPhases stores phase data for the PB lap. Called after Update returns true
// and the caller has computed phases for the new PB lap. No-op if no entry exists.
func SetPhases(pbf File, car, track string, phases []PBPhase) {
	key := Key(car, track)
	if pbf[key] == nil {
		return
	}
	pbf[key].Phases = phases
}

// SetSetup stores the raw "CarSetup:" YAML block from the PB lap's session,
// so "analyze -lap pb" can reproduce the setup tables without the original .ibt.
// No-op if no entry exists.
func SetSetup(pbf File, car, track, setupYAML string) {
	key := Key(car, track)
	if pbf[key] == nil {
		return
	}
	pbf[key].Setup = setupYAML
}
