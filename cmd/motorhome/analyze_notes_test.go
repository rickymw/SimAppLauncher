package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/notes"
)

func TestNotesFileForIbt(t *testing.T) {
	notesDir := filepath.Join("C:", "sim", "notes")

	tests := []struct {
		name    string
		dir     string
		ibt     string
		want    string
		wantAny bool
	}{
		{
			name:    "basic",
			dir:     notesDir,
			ibt:     filepath.Join("D:", "telemetry", "porsche718 watkinsglen 2026-07-30.ibt"),
			want:    filepath.Join(notesDir, "porsche718 watkinsglen 2026-07-30.json"),
			wantAny: true,
		},
		{
			// Windows filesystems are case-insensitive, so an uppercase
			// extension must resolve to the same notes file.
			name:    "uppercase extension",
			dir:     notesDir,
			ibt:     filepath.Join("D:", "telemetry", "SESSION.IBT"),
			want:    filepath.Join(notesDir, "SESSION.json"),
			wantAny: true,
		},
		{name: "no notes dir", dir: "", ibt: "session.ibt", want: ""},
		{name: "no ibt", dir: notesDir, ibt: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := notesFileForIbt(tt.dir, tt.ibt)
			if got != tt.want {
				t.Errorf("notesFileForIbt(%q, %q) = %q, want %q", tt.dir, tt.ibt, got, tt.want)
			}
		})
	}
}

// A session with no notes file is the normal case and must not be an error.
func TestLoadNotesForIbt_Missing(t *testing.T) {
	dir := t.TempDir()
	sess, path, err := loadNotesForIbt(dir, filepath.Join(dir, "nothing.ibt"))
	if err != nil {
		t.Fatalf("unexpected error for a missing notes file: %v", err)
	}
	if path != "" {
		t.Errorf("path = %q, want empty when no notes file exists", path)
	}
	if len(sess.Notes) != 0 {
		t.Errorf("expected no notes, got %d", len(sess.Notes))
	}
}

func TestLoadNotesForIbt_ReadsSession(t *testing.T) {
	dir := t.TempDir()
	ibtPath := filepath.Join(dir, "session.ibt")

	want := notes.Session{
		IbtFile: "session.ibt",
		Start:   time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
		Notes: []notes.Note{
			{Timestamp: time.Date(2026, 7, 30, 14, 1, 0, 0, time.UTC), Text: "hello"},
		},
	}
	if err := notes.SaveSession(filepath.Join(dir, "session.json"), want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	sess, path, err := loadNotesForIbt(dir, ibtPath)
	if err != nil {
		t.Fatalf("loadNotesForIbt: %v", err)
	}
	if path == "" {
		t.Error("expected a non-empty path for an existing notes file")
	}
	if len(sess.Notes) != 1 || sess.Notes[0].Text != "hello" {
		t.Errorf("got notes %+v", sess.Notes)
	}
}

func TestLoadNotesForIbt_Corrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadNotesForIbt(dir, filepath.Join(dir, "session.ibt")); err == nil {
		t.Error("expected an error for a corrupt notes file")
	}
}

// Notes must be anchored on speech start when it is available, since the
// end-of-recording timestamp trails the event by the length of the utterance.
func TestNoteAnchorPrefersStartedAt(t *testing.T) {
	start := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	stop := start.Add(4 * time.Second)

	withStart := notes.Note{StartedAt: start, Timestamp: stop}
	if got := withStart.Anchor(); !got.Equal(start) {
		t.Errorf("Anchor() = %v, want StartedAt %v", got, start)
	}

	legacy := notes.Note{Timestamp: stop}
	if got := legacy.Anchor(); !got.Equal(stop) {
		t.Errorf("Anchor() = %v, want Timestamp %v for a note with no StartedAt", got, stop)
	}
}

func TestLocateSessionNotes_Empty(t *testing.T) {
	if got := locateSessionNotes(notes.Session{}, nil, nil, time.Now(), 0); got != nil {
		t.Errorf("expected nil for a session with no notes, got %+v", got)
	}
}

// captureAnalyzeOut redirects the analyze output sink for the duration of fn.
func captureAnalyzeOut(t *testing.T, fn func()) string {
	t.Helper()
	prev := analyzeOut
	var buf bytes.Buffer
	analyzeOut = &buf
	defer func() { analyzeOut = prev }()
	fn()
	return buf.String()
}

func TestPrintNotes_LocatedAndUnlocated(t *testing.T) {
	located := []analysis.LocatedNote{
		{Text: "rear stepped out", Located: true, LapNumber: 7, LapDistPct: 0.63, SegName: "T5"},
		{Text: "good session", Located: false},
	}

	out := captureAnalyzeOut(t, func() {
		printNotes(located, filepath.Join("notes", "session.json"))
	})

	for _, want := range []string{"rear stepped out", "T5", "63%", "good session", "session.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "1 note falls outside") {
		t.Errorf("expected a singular unlocated-note summary:\n%s", out)
	}
}

func TestPrintNotes_NoNotesPrintsNothing(t *testing.T) {
	if out := captureAnalyzeOut(t, func() { printNotes(nil, "") }); out != "" {
		t.Errorf("expected no output for zero notes, got %q", out)
	}
}

func TestPrintConsistency_NoRowsPrintsNothing(t *testing.T) {
	if out := captureAnalyzeOut(t, func() { printConsistency(nil, nil) }); out != "" {
		t.Errorf("expected no output for zero rows, got %q", out)
	}
}

func TestPrintConsistency_RendersRowsAndRanking(t *testing.T) {
	rows := []analysis.ConsistencyRow{
		{SegName: "T1", Kind: analysis.PhaseEntry, Laps: 4,
			EntrySpeedMean: 182.3, EntrySpeedSD: 1.2,
			ExitSpeedMean: 143.0, ExitSpeedSD: 2.4,
			BestExitSpeedKPH: 146.2, BestExitLap: 7},
		{SegName: "T2", Kind: analysis.PhaseExit, Laps: 4,
			ExitSpeedMean: 120.0, ExitSpeedSD: 9.9,
			BestExitSpeedKPH: 130.0, BestExitLap: 3},
	}

	out := captureAnalyzeOut(t, func() { printConsistency(rows, []int{2, 3, 4, 7}) })

	// The header must name the contributing laps, not just count them — the
	// filtered population is not the same as the full lap list.
	for _, want := range []string{"Consistency (4 laps: 2, 3, 4, 7)", "T1", "T2", "L7", "Most variable"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// T2 has the larger exit-speed spread, so it must lead the ranking.
	idx := strings.Index(out, "Most variable")
	if idx < 0 || !strings.Contains(out[idx:], "T2") {
		t.Errorf("expected T2 first in the ranking:\n%s", out)
	}
}

func TestDumpSegmentAllLapsPath(t *testing.T) {
	got := dumpSegmentAllLapsPath(filepath.Join("C:", "telemetry"), "T3")
	want := filepath.Join("C:", "telemetry", "T3_alllaps.csv")
	if got != want {
		t.Errorf("dumpSegmentAllLapsPath = %q, want %q", got, want)
	}
	if got := dumpSegmentAllLapsPath("", "T3"); got != "T3_alllaps.csv" {
		t.Errorf("dumpSegmentAllLapsPath with no dir = %q, want T3_alllaps.csv", got)
	}
	// It must not collide with the single-lap dump of the same segment: the two
	// have different column sets.
	if dumpSegmentAllLapsPath("", "T3") == dumpSegmentPath("", "T3", 1) {
		t.Error("all-laps dump path collides with the single-lap path")
	}
}

func TestWriteAnalyzeJSON(t *testing.T) {
	res := analyzeResult{
		Schema: analyzeSchema,
		File:   "session.ibt",
		Car:    "Porsche 718 GT4",
		Track:  "Watkins Glen",
		Laps: []jsonLap{
			{Number: 1, Kind: "out lap"},
			{Number: 2, Kind: "flying lap", LapTime: 84.5, TimeFormatted: "1:24.500", Complete: true, Comparable: true},
		},
	}

	var buf bytes.Buffer
	if err := writeAnalyzeJSON(&buf, res); err != nil {
		t.Fatalf("writeAnalyzeJSON: %v", err)
	}

	var back analyzeResult
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if back.Schema != analyzeSchema {
		t.Errorf("schema = %q, want %q", back.Schema, analyzeSchema)
	}
	if len(back.Laps) != 2 || back.Laps[1].TimeFormatted != "1:24.500" {
		t.Errorf("laps did not round-trip: %+v", back.Laps)
	}
}

// The document must stay parseable when the optional sections are absent —
// that is the shape produced by a session with no track map, PB or notes.
func TestWriteAnalyzeJSON_OmitsEmptySections(t *testing.T) {
	var buf bytes.Buffer
	if err := writeAnalyzeJSON(&buf, analyzeResult{Schema: analyzeSchema, Laps: []jsonLap{}}); err != nil {
		t.Fatalf("writeAnalyzeJSON: %v", err)
	}
	out := buf.String()
	for _, absent := range []string{"trackMap", "pb\"", "consistency", "notes", "analysedLap", "sectors"} {
		if strings.Contains(out, absent) {
			t.Errorf("expected %q to be omitted:\n%s", absent, out)
		}
	}
}

func TestWriteAnalyzeJSON_DoesNotEscapeTrackNames(t *testing.T) {
	var buf bytes.Buffer
	res := analyzeResult{Schema: analyzeSchema, Track: "Mount Panorama & Bathurst"}
	if err := writeAnalyzeJSON(&buf, res); err != nil {
		t.Fatalf("writeAnalyzeJSON: %v", err)
	}
	if strings.Contains(buf.String(), `\u0026`) {
		t.Errorf("track name was HTML-escaped:\n%s", buf.String())
	}
}

// io.Discard is what -json binds the table sink to; confirm the printers
// tolerate it rather than assuming an *os.File.
func TestAnalyzeOutDiscard(t *testing.T) {
	prev := analyzeOut
	analyzeOut = io.Discard
	defer func() { analyzeOut = prev }()

	printConsistency([]analysis.ConsistencyRow{{SegName: "T1", Kind: analysis.PhaseFull, Laps: 2}}, []int{1, 2})
	printNotes([]analysis.LocatedNote{{Text: "x", Located: true, LapNumber: 1}}, "")
}
