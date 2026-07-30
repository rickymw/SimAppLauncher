package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/notes"
	"github.com/rickymw/MotorHome/internal/pb"
)

// flowOpts builds a singleLapOpts covering the full per-lap output stage.
func flowOpts() singleLapOpts {
	laps := []analysis.Lap{outputTestLap(2, 102.0), outputTestLap(3, 101.5)}
	segs := outputTestSegs()
	return singleLapOpts{
		laps:           laps,
		segs:           segs,
		comparableLaps: laps,
		consistency:    analysis.ComputeConsistency(laps, segs, nil),
	}
}

func TestAnalyzeSingleLap_BestLap(t *testing.T) {
	out := captureAnalyzeOut(t, func() { analyzeSingleLap(flowOpts()) })

	for _, want := range []string{"Selecting best lap: Lap 3", "1:41.500", "Name", "Corner Exit", "Consistency"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestAnalyzeSingleLap_ExplicitLap(t *testing.T) {
	opts := flowOpts()
	opts.lapNum = 2

	out := captureAnalyzeOut(t, func() { analyzeSingleLap(opts) })

	if strings.Contains(out, "Selecting best lap") {
		t.Errorf("an explicit -lap should not announce a best-lap selection:\n%s", out)
	}
	if !strings.Contains(out, "Lap 2") {
		t.Errorf("expected lap 2 to be rendered:\n%s", out)
	}
}

// Asking for an out lap is legal but the data includes pit lane, so it has to
// be called out rather than presented as a normal lap.
func TestAnalyzeSingleLap_WarnsOnNonFlyingLap(t *testing.T) {
	opts := flowOpts()
	opts.laps[0].Kind = analysis.KindOutLap
	opts.lapNum = 2

	out := captureAnalyzeOut(t, func() { analyzeSingleLap(opts) })
	if !strings.Contains(out, "is a out lap") && !strings.Contains(out, "pit lane") {
		t.Errorf("expected a warning for a non-flying lap:\n%s", out)
	}
}

func TestAnalyzeSingleLap_WarnsOnPartialStart(t *testing.T) {
	opts := flowOpts()
	opts.laps[0].IsPartialStart = true
	opts.lapNum = 2

	out := captureAnalyzeOut(t, func() { analyzeSingleLap(opts) })
	if !strings.Contains(out, "underestimated") {
		t.Errorf("expected a partial-start warning:\n%s", out)
	}
}

// Without a track map there are no named segments, so the output falls back to
// the 20-zone split rather than printing nothing.
func TestAnalyzeSingleLap_NoTrackMapUsesZones(t *testing.T) {
	opts := flowOpts()
	opts.segs = nil
	opts.consistency = nil

	out := captureAnalyzeOut(t, func() { analyzeSingleLap(opts) })
	if !strings.Contains(out, "Zone") {
		t.Errorf("expected the zone fallback:\n%s", out)
	}
	if strings.Contains(out, "Corner Exit") {
		t.Errorf("exit impact needs segments and should be absent:\n%s", out)
	}
}

func TestAnalyzeSingleLap_RendersVsPBAndNotes(t *testing.T) {
	opts := flowOpts()
	lap := outputTestLap(3, 101.5)
	opts.pbPhases = phasesToPB(analysis.ComputePhases(&lap, opts.segs, nil))
	opts.locatedNotes = []analysis.LocatedNote{
		{Text: "loose on exit", Located: true, LapNumber: 3, LapDistPct: 0.6, SegName: "T1"},
	}
	opts.notesSourceFile = filepath.Join("notes", "session.json")

	out := captureAnalyzeOut(t, func() { analyzeSingleLap(opts) })

	if !strings.Contains(out, "vs PB") {
		t.Errorf("expected the vs-PB table:\n%s", out)
	}
	if !strings.Contains(out, "loose on exit") {
		t.Errorf("expected the notes table:\n%s", out)
	}
}

func TestAnalyzeSingleLap_DumpsSegment(t *testing.T) {
	dir := t.TempDir()
	opts := flowOpts()
	opts.dumpSeg = "T1"
	opts.dumpDir = dir

	captureAnalyzeOut(t, func() { analyzeSingleLap(opts) })

	if _, err := os.Stat(filepath.Join(dir, "T1_lap3.csv")); err != nil {
		t.Errorf("expected a dump for the analysed lap: %v", err)
	}
}

// ---- resolveNotes ----

func TestResolveNotes_NoNotesDir(t *testing.T) {
	got, path := resolveNotes("", "session.ibt", nil, nil, time.Now(), 2)
	if got != nil || path != "" {
		t.Errorf("expected no notes with no notes dir, got %v / %q", got, path)
	}
}

func TestResolveNotes_LocatesNotes(t *testing.T) {
	dir := t.TempDir()
	ibtPath := filepath.Join(dir, "session.ibt")
	recStart := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)

	// outputTestLap starts at SessionTime number*1000 and runs 10s at 60Hz.
	laps := []analysis.Lap{outputTestLap(1, 100)}

	sess := notes.Session{
		IbtFile: "session.ibt",
		Notes: []notes.Note{
			// 5s into the recording, with a 2s lag correction applied by the
			// caller → 3s in, which is 30% round the lap → the S1 straight.
			{StartedAt: recStart.Add(5 * time.Second), Text: "brakes feel long"},
		},
	}
	if err := notes.SaveSession(filepath.Join(dir, "session.json"), sess); err != nil {
		t.Fatal(err)
	}

	got, path := resolveNotes(dir, ibtPath, laps, outputTestSegs(), recStart, 2)
	if path == "" {
		t.Error("expected the notes file path to be reported")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 note, got %d", len(got))
	}
	if !got[0].Located {
		t.Fatal("note should have been located inside the recording")
	}
	if got[0].SegName != "S1" {
		t.Errorf("SegName = %q, want S1", got[0].SegName)
	}
	if got[0].Text != "brakes feel long" {
		t.Errorf("Text = %q", got[0].Text)
	}
}

// A broken notes file must not take down an otherwise complete analysis.
func TestResolveNotes_CorruptFileIsNonFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte("{broken"), 0644); err != nil {
		t.Fatal(err)
	}

	got, path := resolveNotes(dir, filepath.Join(dir, "session.ibt"),
		[]analysis.Lap{outputTestLap(1, 100)}, outputTestSegs(), time.Now(), 2)

	if got != nil {
		t.Errorf("expected no notes from a corrupt file, got %v", got)
	}
	if path != "" {
		t.Errorf("a corrupt file should not be reported as a source, got %q", path)
	}
}

// ---- brake entries reach the phase computation ----

// Stored brake-entry positions shift each segment's effective entry point, so
// they have to survive the trip through singleLapOpts into ComputePhases.
func TestAnalyzeSingleLap_UsesBrakeEntries(t *testing.T) {
	opts := flowOpts()
	opts.brakeEntries = pb.BrakeEntryMap{"T1": {Pct: 0.45, LapsUsed: 5}}

	withEntries := captureAnalyzeOut(t, func() { analyzeSingleLap(opts) })

	opts.brakeEntries = nil
	without := captureAnalyzeOut(t, func() { analyzeSingleLap(opts) })

	if withEntries == without {
		t.Error("brake entries did not change the phase boundaries — they are being dropped")
	}
}
