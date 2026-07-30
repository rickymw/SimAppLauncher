package main

import (
	"testing"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

func TestFallback(t *testing.T) {
	if got := fallback("", "(unknown)"); got != "(unknown)" {
		t.Errorf("fallback(\"\") = %q, want (unknown)", got)
	}
	if got := fallback("real", "(unknown)"); got != "real" {
		t.Errorf("fallback(\"real\") = %q, want real", got)
	}
}

func TestSegmentNames(t *testing.T) {
	segs := []trackmap.Segment{{Name: "S1"}, {Name: "T1"}, {Name: "S2"}}
	if got := segmentNames(segs); got != "S1, T1, S2" {
		t.Errorf("segmentNames = %q, want \"S1, T1, S2\"", got)
	}
	if got := segmentNames(nil); got != "" {
		t.Errorf("segmentNames(nil) = %q, want empty", got)
	}
}

func TestLapNumbers(t *testing.T) {
	laps := []analysis.Lap{{Number: 3}, {Number: 5}, {Number: 8}}
	got := lapNumbers(laps)
	want := []int{3, 5, 8}
	if len(got) != len(want) {
		t.Fatalf("lapNumbers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("lapNumbers[%d] = %d, want %d", i, got[i], want[i])
		}
	}
	if got := lapNumbers(nil); len(got) != 0 {
		t.Errorf("lapNumbers(nil) = %v, want empty", got)
	}
}

func TestFindAnalyzeLap(t *testing.T) {
	laps := []analysis.Lap{{Number: 1}, {Number: 2}, {Number: 3}}
	if got := findAnalyzeLap(laps, 2); got == nil || got.Number != 2 {
		t.Errorf("findAnalyzeLap(2) = %v", got)
	}
	if got := findAnalyzeLap(laps, 99); got != nil {
		t.Errorf("findAnalyzeLap(99) = %v, want nil", got)
	}
}

func TestSelectAnalyzeLap(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 102.0, 600, false),
		makeLap(analysis.KindFlying, 101.5, 600, false),
	}
	laps[0].Number, laps[1].Number = 2, 3

	// lapNum 0 means "best of session".
	if got := selectAnalyzeLap(laps, 0); got == nil || got.Number != 3 {
		t.Errorf("selectAnalyzeLap(0) should pick the best lap, got %v", got)
	}
	// A positive lapNum selects that lap even when it isn't the best.
	if got := selectAnalyzeLap(laps, 2); got == nil || got.Number != 2 {
		t.Errorf("selectAnalyzeLap(2) = %v, want lap 2", got)
	}
	if got := selectAnalyzeLap(laps, 99); got != nil {
		t.Errorf("selectAnalyzeLap(99) = %v, want nil", got)
	}
}

// ---- crossLapComparableLaps ----

// The wide filter is the whole reason consistency produces anything on a
// session where the driver was still improving.
func TestCrossLapComparableLaps_WiderThanGeometryFilter(t *testing.T) {
	// Best 101.5; a lap 5.6s slower is outside the 1.5s geometry window but
	// inside the 10% cross-lap window (101.5 * 1.10 = 111.65).
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 121.7, 600, false), // 20% off — excluded
		makeLap(analysis.KindFlying, 107.2, 600, false), // 5.6% off — included
		makeLap(analysis.KindFlying, 101.5, 600, false), // best
	}
	for i := range laps {
		laps[i].Number = i + 2
	}

	got := crossLapComparableLaps(laps, 101.5)
	if len(got) != 2 {
		t.Fatalf("expected 2 comparable laps, got %d: %v", len(got), lapNumbers(got))
	}

	// The geometry filter keeps only the best lap from the same input, which is
	// exactly why the two populations cannot be shared.
	if tight := flyingLapsWithinTime(laps, 101.5); len(tight) >= len(got) {
		t.Errorf("geometry filter (%d laps) should be narrower than the cross-lap filter (%d)",
			len(tight), len(got))
	}
}

func TestCrossLapComparableLaps_ExcludesUnusableLaps(t *testing.T) {
	outLap := makeLap(analysis.KindOutLap, 102.0, 600, false)
	inLap := makeLap(analysis.KindInLap, 102.0, 600, false)
	partial := makeLap(analysis.KindFlying, 102.0, 600, true)
	cut := makeLap(analysis.KindFlying, 102.0, 600, false)
	cut.IsCut = true
	incomplete := makeLap(analysis.KindFlying, 0, 600, false)
	good := makeLap(analysis.KindFlying, 101.5, 600, false)

	laps := []analysis.Lap{outLap, inLap, partial, cut, incomplete, good}
	got := crossLapComparableLaps(laps, 101.5)
	if len(got) != 1 {
		t.Errorf("expected only the clean flying lap, got %d laps", len(got))
	}
}

// A phantom LapLastLapTime shorter than the session median must not be treated
// as a comparable lap — it would poison every spread it touched.
func TestCrossLapComparableLaps_RejectsImplausiblyShortLap(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 101.5, 600, false),
		makeLap(analysis.KindFlying, 102.0, 600, false),
		makeLap(analysis.KindFlying, 30.0, 600, false), // stitched/partial LLT
	}
	for _, l := range crossLapComparableLaps(laps, 101.5) {
		if l.LapTime < 60 {
			t.Errorf("implausibly short lap (%.1fs) was included", l.LapTime)
		}
	}
}

func TestCrossLapComparableLaps_Empty(t *testing.T) {
	if got := crossLapComparableLaps(nil, 100); len(got) != 0 {
		t.Errorf("expected no laps, got %d", len(got))
	}
}
