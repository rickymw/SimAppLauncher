package analysis

import (
	"math"
	"testing"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// consistencyLap builds a lap whose corner is driven at a given entry/exit
// speed, so a set of laps has a known spread.
func consistencyLap(number int, entryMS, exitMS float32) Lap {
	var samples []SampleData
	const n = 600
	for i := 0; i < n; i++ {
		pct := float32(i) / float32(n)
		// Linear speed ramp from entry to exit across the whole lap.
		frac := float32(i) / float32(n-1)
		speed := entryMS + (exitMS-entryMS)*frac
		s := SampleData{
			LapDistPct:  pct,
			SessionTime: float64(number*100) + float64(i)/60.0,
			Speed:       speed,
			Throttle:    1.0,
		}
		// Give the second half enough steering to be a real corner.
		if pct >= 0.5 {
			s.SteeringAngle = 0.5
		}
		samples = append(samples, s)
	}
	return Lap{Number: number, LapTime: 60, Kind: KindFlying, Samples: samples}
}

func consistencySegs() []trackmap.Segment {
	return []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}
}

func TestComputeConsistency_SpreadAcrossLaps(t *testing.T) {
	// Two laps whose straight entry speeds are 100 and 110 km/h-equivalent.
	laps := []Lap{
		consistencyLap(1, 27.7778, 27.7778), // 100 km/h flat
		consistencyLap(2, 30.5556, 30.5556), // 110 km/h flat
	}

	rows := ComputeConsistency(laps, consistencySegs(), nil)
	if len(rows) == 0 {
		t.Fatal("expected consistency rows")
	}

	var s1 *ConsistencyRow
	for i := range rows {
		if rows[i].SegName == "S1" && rows[i].Kind == PhaseFull {
			s1 = &rows[i]
		}
	}
	if s1 == nil {
		t.Fatal("no full phase row for S1")
	}
	if s1.Laps != 2 {
		t.Errorf("S1 Laps = %d, want 2", s1.Laps)
	}
	if math.Abs(float64(s1.EntrySpeedMean)-105) > 0.5 {
		t.Errorf("S1 EntrySpeedMean = %.2f, want ~105", s1.EntrySpeedMean)
	}
	// Sample SD of {100, 110} is 10/sqrt(2) ≈ 7.07.
	if math.Abs(float64(s1.EntrySpeedSD)-7.07) > 0.2 {
		t.Errorf("S1 EntrySpeedSD = %.2f, want ~7.07", s1.EntrySpeedSD)
	}
}

func TestComputeConsistency_TracksBestExitLap(t *testing.T) {
	laps := []Lap{
		consistencyLap(3, 27.7778, 27.7778), // slower exit
		consistencyLap(7, 27.7778, 33.3333), // faster exit
	}

	rows := ComputeConsistency(laps, consistencySegs(), nil)
	found := false
	for _, r := range rows {
		if r.SegName != "S1" {
			continue
		}
		found = true
		if r.BestExitLap != 7 {
			t.Errorf("BestExitLap = %d, want 7", r.BestExitLap)
		}
		if r.BestExitSpeedKPH < r.ExitSpeedMean {
			t.Errorf("BestExitSpeedKPH %.1f should be >= mean %.1f",
				r.BestExitSpeedKPH, r.ExitSpeedMean)
		}
	}
	if !found {
		t.Fatal("no S1 row returned")
	}
}

func TestComputeConsistency_TooFewLaps(t *testing.T) {
	laps := []Lap{consistencyLap(1, 27.7778, 27.7778)}
	if rows := ComputeConsistency(laps, consistencySegs(), nil); rows != nil {
		t.Errorf("expected nil for a single lap, got %d rows", len(rows))
	}
}

func TestComputeConsistency_NoSegments(t *testing.T) {
	laps := []Lap{
		consistencyLap(1, 27.7778, 27.7778),
		consistencyLap(2, 27.7778, 27.7778),
	}
	if rows := ComputeConsistency(laps, nil, nil); rows != nil {
		t.Errorf("expected nil with no track map, got %d rows", len(rows))
	}
}

// A phase seen on only one lap says nothing about consistency, so it must be
// dropped rather than reported with a misleading zero spread.
func TestComputeConsistency_DropsSingleObservationPhases(t *testing.T) {
	// Lap 2's "corner" has no steering at all, so it collapses to a single
	// "full" phase while lap 1 splits into entry/mid/exit.
	flat := consistencyLap(2, 27.7778, 27.7778)
	for i := range flat.Samples {
		flat.Samples[i].SteeringAngle = 0
	}
	laps := []Lap{consistencyLap(1, 27.7778, 27.7778), flat}

	rows := ComputeConsistency(laps, consistencySegs(), nil)
	for _, r := range rows {
		if r.Laps < MinLapsForConsistency {
			t.Errorf("row %s/%s reported with only %d lap(s)", r.SegName, r.Kind, r.Laps)
		}
	}
}

func TestMostVariable(t *testing.T) {
	rows := []ConsistencyRow{
		{SegName: "T1", ExitSpeedSD: 1.0},
		{SegName: "T2", ExitSpeedSD: 5.0},
		{SegName: "T3", ExitSpeedSD: 3.0},
	}
	got := MostVariable(rows, 2)
	if len(got) != 2 {
		t.Fatalf("MostVariable returned %d rows, want 2", len(got))
	}
	if got[0].SegName != "T2" || got[1].SegName != "T3" {
		t.Errorf("MostVariable order = %s, %s; want T2, T3", got[0].SegName, got[1].SegName)
	}
	// The input must not be reordered — callers print it in track order.
	if rows[0].SegName != "T1" {
		t.Error("MostVariable mutated its input slice")
	}
	if MostVariable(rows, 0) != nil {
		t.Error("MostVariable(_, 0) should return nil")
	}
	if len(MostVariable(rows, 99)) != 3 {
		t.Error("MostVariable should clamp n to the number of rows")
	}
}

func TestMeanSD(t *testing.T) {
	tests := []struct {
		name       string
		in         []float32
		mean, sd   float32
		tolerance  float64
		wantZeroSD bool
	}{
		{name: "empty", in: nil, mean: 0, sd: 0, tolerance: 0.001},
		{name: "single", in: []float32{5}, mean: 5, sd: 0, tolerance: 0.001},
		{name: "pair", in: []float32{10, 20}, mean: 15, sd: 7.0711, tolerance: 0.001},
		{name: "identical", in: []float32{3, 3, 3}, mean: 3, sd: 0, tolerance: 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mean, sd := meanSD(tt.in)
			if math.Abs(float64(mean-tt.mean)) > tt.tolerance {
				t.Errorf("mean = %v, want %v", mean, tt.mean)
			}
			if math.Abs(float64(sd-tt.sd)) > tt.tolerance {
				t.Errorf("sd = %v, want %v", sd, tt.sd)
			}
		})
	}
}
