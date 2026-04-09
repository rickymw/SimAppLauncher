package trackmap

import (
	"encoding/json"
	"os"
)

// TrackRef holds reference metadata for a known track. Used to guide
// segment detection toward the expected corner count.
type TrackRef struct {
	// Corners is the expected number of corner segments (a chicane counts as 1).
	Corners int `json:"corners"`

	// Comment is a human-readable description of the corner list.
	Comment string `json:"comment,omitempty"`
}

// TrackRefFile is the top-level type for trackref.json: a map from
// iRacing TrackDisplayName to reference metadata.
type TrackRefFile map[string]*TrackRef

// LoadTrackRef reads trackref.json from the given path. Returns an empty map
// (not nil) if the file doesn't exist. Returns an error only on malformed JSON.
func LoadTrackRef(path string) (TrackRefFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return TrackRefFile{}, nil
		}
		return nil, err
	}
	var trf TrackRefFile
	if err := json.Unmarshal(data, &trf); err != nil {
		return nil, err
	}
	if trf == nil {
		trf = TrackRefFile{}
	}
	return trf, nil
}

// Corners returns the expected corner segment count for the given track, and
// whether a reference exists.
func (trf TrackRefFile) Corners(trackName string) (int, bool) {
	ref, ok := trf[trackName]
	if !ok || ref == nil {
		return 0, false
	}
	return ref.Corners, true
}
