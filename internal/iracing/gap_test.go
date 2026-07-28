package iracing

import (
	"math"
	"testing"
)

// At 90s lap pace, 0.01 of a lap ≈ 0.9s. Asserted with a small tolerance.
const (
	defaultLap = float32(90)
	gapEpsilon = float32(0.05)
)

func TestComputeGaps_SameLapAheadAndBehind(t *testing.T) {
	me := CarPos{CarIdx: 2, LapDistPct: 0.50, LapCompleted: 3}
	others := []CarPos{
		{CarIdx: 1, LapDistPct: 0.60, LapCompleted: 3}, // 10% ahead
		{CarIdx: 3, LapDistPct: 0.40, LapCompleted: 3}, // 10% behind
		{CarIdx: 4, LapDistPct: 0.80, LapCompleted: 3}, // further ahead
	}
	ahead, behind := ComputeGaps(me, others, defaultLap)
	if ahead.CarIdx != 1 {
		t.Errorf("ahead car: got %d want 1", ahead.CarIdx)
	}
	if behind.CarIdx != 3 {
		t.Errorf("behind car: got %d want 3", behind.CarIdx)
	}
	if !approx(ahead.TimeSeconds, 9.0, gapEpsilon) {
		t.Errorf("ahead time: got %.3f want ~9.0", ahead.TimeSeconds)
	}
	if !approx(behind.TimeSeconds, -9.0, gapEpsilon) {
		t.Errorf("behind time: got %.3f want ~-9.0", behind.TimeSeconds)
	}
}

func TestComputeGaps_WrapsAroundSFLine(t *testing.T) {
	// Me at 0.99, ahead car at 0.01 (just past S/F) — 2% ahead, wrapping.
	// Behind car at 0.97 — 2% behind, no wrap.
	me := CarPos{CarIdx: 0, LapDistPct: 0.99, LapCompleted: 5}
	others := []CarPos{
		{CarIdx: 1, LapDistPct: 0.01, LapCompleted: 6}, // wrapped; one lap ahead
		{CarIdx: 2, LapDistPct: 0.97, LapCompleted: 5}, // just behind
	}
	ahead, behind := ComputeGaps(me, others, defaultLap)
	if ahead.CarIdx != 1 {
		t.Errorf("ahead: got %d want 1", ahead.CarIdx)
	}
	if ahead.LapsDelta != 1 {
		t.Errorf("ahead LapsDelta: got %d want 1", ahead.LapsDelta)
	}
	if !approx(ahead.DistPct, 0.02, 1e-4) {
		t.Errorf("ahead DistPct: got %.4f want 0.02", ahead.DistPct)
	}
	if behind.CarIdx != 2 {
		t.Errorf("behind: got %d want 2", behind.CarIdx)
	}
}

func TestComputeGaps_SkipsInvalidAndSelf(t *testing.T) {
	me := CarPos{CarIdx: 2, LapDistPct: 0.5}
	others := []CarPos{
		{CarIdx: 2, LapDistPct: 0.5},  // same as me — must be skipped
		{CarIdx: 3, LapDistPct: -1},   // invalid — must be skipped
		{CarIdx: 4, LapDistPct: 0.55}, // only valid candidate
	}
	ahead, behind := ComputeGaps(me, others, defaultLap)
	if ahead.CarIdx != 4 {
		t.Errorf("ahead: got %d want 4", ahead.CarIdx)
	}
	if behind.CarIdx != 4 {
		t.Errorf("behind: got %d want 4 (only valid candidate)", behind.CarIdx)
	}
}

func TestComputeGaps_UsesEstTimeWhenAvailable(t *testing.T) {
	// Same lap, populated EstTimes — the output should use those directly
	// rather than distPct * lapEstimate.
	me := CarPos{CarIdx: 0, LapDistPct: 0.50, EstTime: 45.0}
	others := []CarPos{
		{CarIdx: 1, LapDistPct: 0.55, EstTime: 48.3}, // 3.3s ahead per EstTime
	}
	ahead, _ := ComputeGaps(me, others, defaultLap)
	if !approx(ahead.TimeSeconds, 3.3, 1e-3) {
		t.Errorf("should use EstTime diff: got %.3f want 3.3", ahead.TimeSeconds)
	}
}

func TestComputeGaps_EmptyInputReturnsNoGap(t *testing.T) {
	ahead, behind := ComputeGaps(CarPos{CarIdx: -1}, nil, defaultLap)
	if ahead != NoGap || behind != NoGap {
		t.Errorf("want NoGap, got ahead=%+v behind=%+v", ahead, behind)
	}
}

func TestComputeGaps_NoOthersReturnsNoGap(t *testing.T) {
	// Player exists but the field is empty (e.g. solo practice). Both gaps
	// must be the NoGap sentinel so the formatter does not mistake a valid
	// gap to CarIdx 0 (the pace car slot) for an absent gap.
	me := CarPos{CarIdx: 3, LapDistPct: 0.5}
	ahead, behind := ComputeGaps(me, nil, defaultLap)
	if ahead != NoGap || behind != NoGap {
		t.Errorf("want NoGap, got ahead=%+v behind=%+v", ahead, behind)
	}
}

func TestComputeGaps_CarIdxZeroIsValidNotSentinel(t *testing.T) {
	// CarIdx 0 (often the pace car) must be reportable as a real gap target
	// — the previous zero-value sentinel collided with this case.
	me := CarPos{CarIdx: 5, LapDistPct: 0.50}
	others := []CarPos{
		{CarIdx: 0, LapDistPct: 0.60, LapCompleted: 3}, // 10% ahead
	}
	ahead, _ := ComputeGaps(me, others, defaultLap)
	if ahead.CarIdx != 0 {
		t.Errorf("ahead CarIdx: got %d want 0 (real slot)", ahead.CarIdx)
	}
	if ahead == NoGap {
		t.Errorf("ahead must not equal NoGap when a real candidate exists")
	}
}

func approx(a, b, eps float32) bool {
	return float32(math.Abs(float64(a-b))) < eps
}
