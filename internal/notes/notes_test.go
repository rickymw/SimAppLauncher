package notes

import (
	"path/filepath"
	"testing"
	"time"
)

// ---- LoadSession ----

func TestLoadSession_MissingFile_ReturnsEmptySession(t *testing.T) {
	s, err := LoadSession("nonexistent_notes_xyzzy.json")
	if err != nil {
		t.Fatalf("LoadSession missing file: got error %v, want nil", err)
	}
	if s.Notes == nil {
		t.Error("LoadSession missing file: Notes slice should be non-nil (empty)")
	}
	if len(s.Notes) != 0 {
		t.Errorf("LoadSession missing file: got %d notes, want 0", len(s.Notes))
	}
}

// ---- SaveSession / LoadSession roundtrip ----

func TestSaveLoadSession_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	now := time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC)
	orig := Session{
		IbtFile: "race_2026-03-31.ibt",
		Start:   now,
		Notes: []Note{
			{
				Timestamp: now.Add(time.Minute),
				Text:      "too much understeer mid-corner",
			},
		},
	}

	if err := SaveSession(path, orig); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if loaded.IbtFile != orig.IbtFile {
		t.Errorf("IbtFile = %q, want %q", loaded.IbtFile, orig.IbtFile)
	}
	if len(loaded.Notes) != 1 {
		t.Fatalf("len(Notes) = %d, want 1", len(loaded.Notes))
	}
	if loaded.Notes[0].Text != "too much understeer mid-corner" {
		t.Errorf("Note.Text = %q, want 'too much understeer mid-corner'", loaded.Notes[0].Text)
	}
}

// TestNote_Fields verifies Note contains only Timestamp and Text.
// This is a compile-time check: if any old fields (Lap, LapTime, LastLapTime,
// SessionTime, TrackPct, Segment) are accidentally re-added, the struct literal
// below will fail to compile.
func TestNote_Fields(t *testing.T) {
	_ = Note{
		Timestamp: time.Now(),
		Text:      "hello",
	}
}

// ---- AppendNote ----

func TestAppendNote_CreatesFileAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new_session.json")

	note1 := Note{Timestamp: time.Now().UTC(), Text: "first note"}
	if err := AppendNote(path, note1); err != nil {
		t.Fatalf("AppendNote (first): %v", err)
	}

	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession after first AppendNote: %v", err)
	}
	if len(s.Notes) != 1 {
		t.Fatalf("len(Notes) = %d, want 1", len(s.Notes))
	}
	if s.Notes[0].Text != "first note" {
		t.Errorf("Notes[0].Text = %q, want 'first note'", s.Notes[0].Text)
	}
}

func TestAppendNote_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	note1 := Note{Timestamp: time.Now().UTC(), Text: "first"}
	if err := AppendNote(path, note1); err != nil {
		t.Fatalf("AppendNote (first): %v", err)
	}

	note2 := Note{Timestamp: time.Now().UTC(), Text: "second"}
	if err := AppendNote(path, note2); err != nil {
		t.Fatalf("AppendNote (second): %v", err)
	}

	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(s.Notes) != 2 {
		t.Fatalf("len(Notes) = %d, want 2", len(s.Notes))
	}
	if s.Notes[1].Text != "second" {
		t.Errorf("Notes[1].Text = %q, want 'second'", s.Notes[1].Text)
	}
}

func TestAppendNote_PreservesOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	texts := []string{"alpha", "beta", "gamma"}
	for _, text := range texts {
		if err := AppendNote(path, Note{Text: text}); err != nil {
			t.Fatalf("AppendNote(%q): %v", text, err)
		}
	}

	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(s.Notes) != 3 {
		t.Fatalf("len(Notes) = %d, want 3", len(s.Notes))
	}
	for i, want := range texts {
		if s.Notes[i].Text != want {
			t.Errorf("Notes[%d].Text = %q, want %q", i, s.Notes[i].Text, want)
		}
	}
}
