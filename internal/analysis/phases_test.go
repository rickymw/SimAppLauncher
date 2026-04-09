package analysis

import (
	"math"
	"testing"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// cornerSample returns a sample at pct with the given steering angle (degrees)
// and speed (m/s). Brake and throttle default to zero.
func cornerSample(pct float32, t float64, steerDeg, speed float32) SampleData {
	return SampleData{
		LapDistPct:    pct,
		SessionTime:   t,
		Speed:         speed,
		SteeringAngle: steerDeg / rad2deg, // convert to radians
		LFspeed:       speed,
		RFspeed:       speed,
		LRspeed:       speed,
		RRspeed:       speed,
	}
}

func TestComputePhases_StraightGetsFull(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 1.0},
	}
	samples := make([]SampleData, 200)
	for i := range samples {
		pct := float32(i) / float32(len(samples))
		samples[i] = SampleData{
			LapDistPct:  pct,
			SessionTime: float64(i) / 60,
			Speed:       50,
			Throttle:    1.0,
		}
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs)

	if len(phases) != 1 {
		t.Fatalf("len(phases) = %d, want 1", len(phases))
	}
	if phases[0].Kind != PhaseFull {
		t.Errorf("phase kind = %q, want %q", phases[0].Kind, PhaseFull)
	}
	if phases[0].SegName != "S1" {
		t.Errorf("phase seg name = %q, want S1", phases[0].SegName)
	}
}

func TestComputePhases_CornerSplitsOnSteering(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.0, ExitPct: 1.0},
	}

	// Build a steering trace: ramp up to 90° at mid-segment, then ramp down.
	// 80% threshold = 72°.
	n := 200
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		// Triangle steering profile: 0→90°→0° over the segment.
		var steerDeg float32
		if i < n/2 {
			steerDeg = 90.0 * float32(i) / float32(n/2)
		} else {
			steerDeg = 90.0 * float32(n-i) / float32(n/2)
		}
		samples[i] = cornerSample(pct, float64(i)/60, steerDeg, 30)
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs)

	// Should have entry, mid, exit.
	if len(phases) < 2 {
		t.Fatalf("len(phases) = %d, want >= 2 (entry/mid/exit)", len(phases))
	}

	kinds := make(map[PhaseKind]bool)
	for _, p := range phases {
		kinds[p.Kind] = true
	}
	if !kinds[PhaseEntry] {
		t.Error("missing entry phase")
	}
	if !kinds[PhaseMid] {
		t.Error("missing mid phase")
	}
	if !kinds[PhaseExit] {
		t.Error("missing exit phase")
	}
}

func TestComputePhases_LowSteeringCornerGetsFull(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.0, ExitPct: 1.0},
	}

	// Peak steering only 3° — below the 5° threshold.
	n := 100
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		samples[i] = cornerSample(pct, float64(i)/60, 3.0, 50)
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs)

	if len(phases) != 1 {
		t.Fatalf("len(phases) = %d, want 1", len(phases))
	}
	if phases[0].Kind != PhaseFull {
		t.Errorf("phase kind = %q, want %q", phases[0].Kind, PhaseFull)
	}
}

func TestComputePhases_SteeringMetrics(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.0, ExitPct: 1.0},
	}

	n := 200
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		var steerDeg float32
		if i < n/2 {
			steerDeg = 90.0 * float32(i) / float32(n/2)
		} else {
			steerDeg = 90.0 * float32(n-i) / float32(n/2)
		}
		samples[i] = cornerSample(pct, float64(i)/60, steerDeg, 30)
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs)

	// The mid phase should have peak steer near 90°.
	for _, p := range phases {
		if p.Kind == PhaseMid {
			if math.Abs(float64(p.PeakSteerDeg-90)) > 5 {
				t.Errorf("mid PeakSteerDeg = %.1f, want ~90", p.PeakSteerDeg)
			}
		}
	}
}

func TestComputePhases_PhaseStats(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}

	n := 200
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		s := SampleData{
			LapDistPct:  pct,
			SessionTime: float64(i) / 60,
			Speed:       40,
			Throttle:    1.0,
			ThrottleRaw: 1.0,
			LFspeed:     40, RFspeed: 40, LRspeed: 40, RRspeed: 40,
		}
		if pct >= 0.5 {
			// Corner: add steering, braking, reduce throttle.
			s.SteeringAngle = 45.0 / rad2deg
			s.Brake = 0.5
			s.Throttle = 0.0
			s.Speed = 20
		}
		samples[i] = s
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs)

	// Straight should be full throttle.
	for _, p := range phases {
		if p.SegName == "S1" && p.Kind == PhaseFull {
			if p.ThrottlePct < 99 {
				t.Errorf("S1 ThrottlePct = %.0f, want ~100", p.ThrottlePct)
			}
			if p.BrakePct > 1 {
				t.Errorf("S1 BrakePct = %.0f, want ~0", p.BrakePct)
			}
		}
	}

	// Corner phases should have braking.
	var totalCornerSamples int
	for _, p := range phases {
		if p.SegName == "T1" {
			totalCornerSamples += p.SampleCount
		}
	}
	if totalCornerSamples == 0 {
		t.Error("no corner samples found")
	}
}

func TestCountSteeringCorrections_NoCorrections(t *testing.T) {
	// Smooth ramp: no direction reversals.
	samples := make([]SampleData, 100)
	for i := range samples {
		samples[i] = SampleData{SteeringAngle: float32(i) * 0.01}
	}
	if got := countSteeringCorrections(samples); got != 0 {
		t.Errorf("corrections = %d, want 0", got)
	}
}

func TestCountSteeringCorrections_WithCorrections(t *testing.T) {
	// Steering oscillates rapidly: should detect corrections.
	// Each swing is big enough (> 0.5 deg/tick threshold).
	samples := make([]SampleData, 20)
	for i := range samples {
		// Oscillate: 0, +5°, 0, +5°, ... in radians
		if i%2 == 0 {
			samples[i] = SampleData{SteeringAngle: 0}
		} else {
			samples[i] = SampleData{SteeringAngle: 10.0 / rad2deg} // 10° per tick
		}
	}
	got := countSteeringCorrections(samples)
	if got == 0 {
		t.Error("corrections = 0, want > 0")
	}
}

func TestComputePhases_TwoSegments(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}

	n := 200
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		var steerDeg float32
		if pct >= 0.5 {
			// Triangle within corner: 0→60°→0°
			cornerFrac := (pct - 0.5) / 0.5
			if cornerFrac < 0.5 {
				steerDeg = 60.0 * cornerFrac / 0.5
			} else {
				steerDeg = 60.0 * (1.0 - cornerFrac) / 0.5
			}
		}
		samples[i] = cornerSample(pct, float64(i)/60, steerDeg, 40)
	}
	lap := makeFlyingLap(samples)
	phases := ComputePhases(&lap, segs)

	// Should have: S1 full, T1 entry, T1 mid, T1 exit (3-4 phases total)
	if len(phases) < 2 {
		t.Fatalf("len(phases) = %d, want >= 2", len(phases))
	}
	if phases[0].Kind != PhaseFull {
		t.Errorf("first phase kind = %q, want %q", phases[0].Kind, PhaseFull)
	}
	if phases[0].SegName != "S1" {
		t.Errorf("first phase name = %q, want S1", phases[0].SegName)
	}
}
