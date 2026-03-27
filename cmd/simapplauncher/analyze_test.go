package main

import (
	"testing"

	"github.com/rickymw/SimAppLauncher/internal/analysis"
)

// makeLap builds a minimal analysis.Lap for bestAnalyzeLap testing.
func makeLap(kind analysis.LapKind, lapTime float32, samples int, partialStart bool) analysis.Lap {
	s := make([]analysis.SampleData, samples)
	return analysis.Lap{
		Kind:           kind,
		LapTime:        lapTime,
		IsPartialStart: partialStart,
		Samples:        s,
	}
}

func TestBestAnalyzeLap_UsesLapTime(t *testing.T) {
	// Lap 1: slower lap but fewer samples (old proxy would have picked this).
	// Lap 2: faster lap but more samples.
	// bestAnalyzeLap must select Lap 2 (lower LapTime), not Lap 1.
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 120.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 115.0, analysis.MinSamplesForValidLap+100, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil, want fastest lap")
	}
	if got.LapTime != 115.0 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 115.0 (fastest lap must be selected by LapTime)", got.LapTime)
	}
}

func TestBestAnalyzeLap_ExcludesPartialStart(t *testing.T) {
	// Lap 1: fastest, but started mid-recording — must be excluded.
	// Lap 2: slower, but a clean full lap.
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 110.0, analysis.MinSamplesForValidLap, true),
		makeLap(analysis.KindFlying, 115.0, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil, want non-partial-start lap")
	}
	if got.LapTime != 115.0 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 115.0 (partial-start lap must be excluded)", got.LapTime)
	}
}

func TestBestAnalyzeLap_ExcludesNonFlying(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindOutLap, 100.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindInLap, 100.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 120.0, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil, want flying lap")
	}
	if got.Kind != analysis.KindFlying {
		t.Errorf("bestAnalyzeLap: Kind = %v, want flying", got.Kind)
	}
}

func TestBestAnalyzeLap_ExcludesZeroLapTime(t *testing.T) {
	// A flying lap with LapTime == 0 must be skipped (timing data is missing).
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 120.0, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil")
	}
	if got.LapTime != 120.0 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 120.0 (zero LapTime must be excluded)", got.LapTime)
	}
}

func TestBestAnalyzeLap_ExcludesBelowMinSamples(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 100.0, analysis.MinSamplesForValidLap-1, false),
		makeLap(analysis.KindFlying, 120.0, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil")
	}
	if got.LapTime != 120.0 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 120.0 (under-sample lap must be excluded)", got.LapTime)
	}
}

func TestBestAnalyzeLap_AllExcluded(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindOutLap, 100.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 100.0, analysis.MinSamplesForValidLap, true), // partial start
	}
	got := bestAnalyzeLap(laps)
	if got != nil {
		t.Errorf("bestAnalyzeLap: got non-nil, want nil when no valid laps exist")
	}
}
