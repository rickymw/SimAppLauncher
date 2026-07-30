package analysis

import (
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// noteTestLaps builds two 10-second laps starting at SessionTime 100, so a
// note's mapping can be checked against a known wall clock.
func noteTestLaps() []Lap {
	mk := func(number int, t0 float64) Lap {
		var samples []SampleData
		const n = 600 // 10s at 60Hz
		for i := 0; i < n; i++ {
			samples = append(samples, SampleData{
				LapDistPct:  float32(i) / float32(n),
				SessionTime: t0 + float64(i)/60.0,
				Speed:       50,
			})
		}
		return Lap{Number: number, LapTime: 10, Kind: KindFlying, Samples: samples}
	}
	return []Lap{mk(1, 100), mk(2, 110)}
}

func noteTestSegs() []trackmap.Segment {
	return []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}
}

func TestLocateNotes_PlacesNoteOnLapAndSegment(t *testing.T) {
	recStart := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	laps := noteTestLaps()

	// 15s into the recording is 5s into lap 2, i.e. halfway round → T1.
	note := NoteInput{At: recStart.Add(15 * time.Second), Text: "rear stepped out"}

	got := LocateNotes(laps, noteTestSegs(), recStart, 0, []NoteInput{note})
	if len(got) != 1 {
		t.Fatalf("expected 1 located note, got %d", len(got))
	}
	n := got[0]
	if !n.Located {
		t.Fatal("note should have been located inside the recording")
	}
	if n.LapNumber != 2 {
		t.Errorf("LapNumber = %d, want 2", n.LapNumber)
	}
	if n.SegName != "T1" {
		t.Errorf("SegName = %q, want T1", n.SegName)
	}
	if n.IntoRecording < 14.9 || n.IntoRecording > 15.1 {
		t.Errorf("IntoRecording = %.2f, want ~15", n.IntoRecording)
	}
	if n.Text != "rear stepped out" {
		t.Errorf("Text = %q", n.Text)
	}
}

// The lag correction is the whole point of the anchor: without it a note lands
// after the event it describes.
func TestLocateNotes_LagShiftsNoteEarlier(t *testing.T) {
	recStart := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	laps := noteTestLaps()

	// 12s in is 2s into lap 2 (20% round → S1). A 4s lag pulls it back to 8s,
	// which is 8s into lap 1 (80% round → T1).
	note := NoteInput{At: recStart.Add(12 * time.Second), Text: "understeer"}

	noLag := LocateNotes(laps, noteTestSegs(), recStart, 0, []NoteInput{note})[0]
	if noLag.LapNumber != 2 || noLag.SegName != "S1" {
		t.Fatalf("without lag: lap %d %s, want lap 2 S1", noLag.LapNumber, noLag.SegName)
	}

	withLag := LocateNotes(laps, noteTestSegs(), recStart, 4*time.Second, []NoteInput{note})[0]
	if withLag.LapNumber != 1 || withLag.SegName != "T1" {
		t.Errorf("with 4s lag: lap %d %s, want lap 1 T1", withLag.LapNumber, withLag.SegName)
	}
}

func TestLocateNotes_OutsideRecording(t *testing.T) {
	recStart := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	laps := noteTestLaps()

	notes := []NoteInput{
		{At: recStart.Add(-30 * time.Second), Text: "before"},
		{At: recStart.Add(5 * time.Minute), Text: "after"},
	}
	got := LocateNotes(laps, noteTestSegs(), recStart, 0, notes)
	if len(got) != 2 {
		t.Fatalf("expected 2 notes back, got %d", len(got))
	}
	for _, n := range got {
		if n.Located {
			t.Errorf("note %q should be unlocated", n.Text)
		}
		// Text must survive even when the position does not.
		if n.Text == "" {
			t.Error("unlocated note lost its text")
		}
	}
}

func TestLocateNotes_SortsByTimestamp(t *testing.T) {
	recStart := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	laps := noteTestLaps()

	notes := []NoteInput{
		{At: recStart.Add(15 * time.Second), Text: "second"},
		{At: recStart.Add(2 * time.Second), Text: "first"},
	}
	got := LocateNotes(laps, noteTestSegs(), recStart, 0, notes)
	if got[0].Text != "first" || got[1].Text != "second" {
		t.Errorf("notes not sorted by time: %q then %q", got[0].Text, got[1].Text)
	}
}

func TestLocateNotes_NoSegmentsStillResolvesLap(t *testing.T) {
	recStart := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	laps := noteTestLaps()

	got := LocateNotes(laps, nil, recStart, 0,
		[]NoteInput{{At: recStart.Add(15 * time.Second), Text: "x"}})
	if !got[0].Located {
		t.Fatal("note should still be located without a track map")
	}
	if got[0].LapNumber != 2 {
		t.Errorf("LapNumber = %d, want 2", got[0].LapNumber)
	}
	if got[0].SegName != "" || got[0].SegIndex != -1 {
		t.Errorf("expected no segment, got %q/%d", got[0].SegName, got[0].SegIndex)
	}
}

func TestLocateNotes_NoLaps(t *testing.T) {
	recStart := time.Now()
	got := LocateNotes(nil, nil, recStart, 0, []NoteInput{{At: recStart, Text: "x"}})
	if len(got) != 1 || got[0].Located {
		t.Error("with no laps every note should come back unlocated")
	}
}

func TestNearestSampleByTime(t *testing.T) {
	samples := []SampleData{
		{SessionTime: 0, LapDistPct: 0.0},
		{SessionTime: 1, LapDistPct: 0.1},
		{SessionTime: 2, LapDistPct: 0.2},
	}
	tests := []struct {
		st   float64
		want float32
	}{
		{-5, 0.0},  // before the start clamps to the first sample
		{0.4, 0.0}, // rounds down
		{0.6, 0.1}, // rounds up
		{1.5, 0.1}, // exact midpoint takes the earlier sample
		{99, 0.2},  // past the end clamps to the last sample
	}
	for _, tt := range tests {
		if got := nearestSampleByTime(samples, tt.st); got.LapDistPct != tt.want {
			t.Errorf("nearestSampleByTime(%v) = %v, want %v", tt.st, got.LapDistPct, tt.want)
		}
	}
}
