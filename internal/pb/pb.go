// Package pb tracks personal-best lap times per car/track combination.
// Results are persisted to a JSON file (pb.json) next to the binary.
package pb

import (
	"encoding/json"
	"os"
)

// PersonalBest holds the fastest recorded lap for a single car/track combo.
type PersonalBest struct {
	LapTime          float32 `json:"lapTime"`          // seconds
	LapTimeFormatted string  `json:"lapTimeFormatted"` // e.g. "2:11.367"
	Date             string  `json:"date"`             // "YYYY-MM-DD"
	Weather          string  `json:"weather"`          // e.g. "Partly Cloudy, 27°C"
	Car              string  `json:"car"`
	Track            string  `json:"track"`
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
func Save(path string, pbf File) error {
	b, err := json.MarshalIndent(pbf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

// Update checks whether lapTime beats the stored PB for the given car/track.
// If so (or if no PB exists yet), it updates pbf in-place and returns true.
// date should be "YYYY-MM-DD"; weather is a human-readable string or "".
func Update(pbf File, car, track string, lapTime float32, formatted, date, weather string) bool {
	key := Key(car, track)
	existing, ok := pbf[key]
	if ok && existing.LapTime <= lapTime {
		return false
	}
	pbf[key] = &PersonalBest{
		LapTime:          lapTime,
		LapTimeFormatted: formatted,
		Date:             date,
		Weather:          weather,
		Car:              car,
		Track:            track,
	}
	return true
}
