// Package notes persists voice notes captured during sim racing sessions.
// Each session gets its own JSON file stored in a notes/ subdirectory.
package notes

import (
	"encoding/json"
	"os"
	"time"
)

// Note holds a single voice note captured during a session.
type Note struct {
	Timestamp time.Time `json:"timestamp"` // UTC moment of key-release
	Text      string    `json:"text"`      // Whisper transcription
}

// Session is the top-level structure for a single notes file.
// It holds session-level metadata and the ordered list of notes.
type Session struct {
	IbtFile string    `json:"ibtFile,omitempty"` // basename of associated .ibt file; "" if none found
	Start   time.Time `json:"start"`             // UTC time the session file was created
	Notes   []Note    `json:"notes"`
}

// LoadSession reads a session file from path.
// Returns an empty Session if the file does not exist.
func LoadSession(path string) (Session, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Session{Notes: []Note{}}, nil
	}
	if err != nil {
		return Session{}, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return Session{}, err
	}
	if s.Notes == nil {
		s.Notes = []Note{}
	}
	return s, nil
}

// SaveSession writes s to path as indented JSON.
func SaveSession(path string, s Session) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

// AppendNote loads the session at path, appends note, and saves.
// If the file does not exist, it is created with an empty Session.
func AppendNote(path string, note Note) error {
	s, err := LoadSession(path)
	if err != nil {
		return err
	}
	s.Notes = append(s.Notes, note)
	return SaveSession(path, s)
}
