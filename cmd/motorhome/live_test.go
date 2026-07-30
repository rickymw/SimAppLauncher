//go:build windows

package main

import (
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/iracing"
)

// liveFixture builds a three-car snapshot: the player (idx 1) mid-lap with one
// car ahead and one behind.
func liveFixture() iracing.LiveData {
	return iracing.LiveData{
		Connected:           true,
		Track:               "Watkins Glen",
		Car:                 "Porsche 718 GT4",
		LapDistPct:          0.50,
		MyCarIdx:            1,
		CarIdxLapDistPct:    []float32{0.60, 0.50, 0.40},
		CarIdxLapCompleted:  []int32{3, 3, 3},
		CarIdxEstTime:       []float32{66, 55, 44},
		CarIdxPosition:      []int32{1, 2, 3},
		CarIdxClassPosition: []int32{1, 2, 3},
		Drivers: map[int32]iracing.DriverInfo{
			0: {CarIdx: 0, UserName: "Ahead Driver", CarNumber: "11", CarClassID: 84},
			1: {CarIdx: 1, UserName: "Ricky Maw", CarNumber: "22", CarClassID: 84},
			2: {CarIdx: 2, UserName: "Behind Driver", CarNumber: "33", CarClassID: 84},
		},
	}
}

func TestIdxValue(t *testing.T) {
	arr := []int32{10, 20, 30}
	if got := idxValue(arr, 1); got != 20 {
		t.Errorf("idxValue(1) = %d, want 20", got)
	}
	// Out-of-range and negative indices must return zero rather than panic:
	// iRacing publishes -1 for an unresolved CarIdx.
	if got := idxValue(arr, -1); got != 0 {
		t.Errorf("idxValue(-1) = %d, want 0", got)
	}
	if got := idxValue(arr, 99); got != 0 {
		t.Errorf("idxValue(99) = %d, want 0", got)
	}
	if got := idxValue(nil, 0); got != 0 {
		t.Errorf("idxValue(nil) = %d, want 0", got)
	}
}

func TestIdxValueF(t *testing.T) {
	arr := []float32{1.5, 2.5}
	if got := idxValueF(arr, 0); got != 1.5 {
		t.Errorf("idxValueF(0) = %v, want 1.5", got)
	}
	if got := idxValueF(arr, -1); got != 0 {
		t.Errorf("idxValueF(-1) = %v, want 0", got)
	}
	if got := idxValueF(arr, 5); got != 0 {
		t.Errorf("idxValueF(5) = %v, want 0", got)
	}
}

// iRacing fills unused car slots with -1, so those must not count towards the
// grid size.
func TestCountValidCars(t *testing.T) {
	if got := countValidCars([]float32{0.1, -1, 0.5, -1, 0.9}); got != 3 {
		t.Errorf("countValidCars = %d, want 3", got)
	}
	if got := countValidCars(nil); got != 0 {
		t.Errorf("countValidCars(nil) = %d, want 0", got)
	}
	// 0.0 is the S/F line, a valid position.
	if got := countValidCars([]float32{0}); got != 1 {
		t.Errorf("countValidCars([0]) = %d, want 1", got)
	}
}

func TestAbsFloat(t *testing.T) {
	if got := absFloat(-2.5); got != 2.5 {
		t.Errorf("absFloat(-2.5) = %v", got)
	}
	if got := absFloat(2.5); got != 2.5 {
		t.Errorf("absFloat(2.5) = %v", got)
	}
	if got := absFloat(0); got != 0 {
		t.Errorf("absFloat(0) = %v", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 20); got != "short" {
		t.Errorf("truncate kept = %q", got)
	}
	if got := truncate("a very long driver name indeed", 10); got != "a very lon" {
		t.Errorf("truncate = %q, want \"a very lon\"", got)
	}
	if got := truncate("exact", 5); got != "exact" {
		t.Errorf("truncate at exact length = %q", got)
	}
}

func TestFormatPositionLine(t *testing.T) {
	got := formatPositionLine(liveFixture())

	// Position 2 of 3 valid cars, lap 4 (3 completed + 1), half way round.
	for _, want := range []string{"Pos 2/3", "Lap 4", "50.0%"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatPositionLine = %q, missing %q", got, want)
		}
	}
	if !strings.Contains(got, "class 2/3") {
		t.Errorf("expected a class position, got %q", got)
	}
}

// Before the first S/F crossing iRacing reports -1 completed laps; that is the
// out lap, displayed as lap 1 rather than lap 0.
func TestFormatPositionLine_BeforeFirstCrossing(t *testing.T) {
	ld := liveFixture()
	ld.CarIdxLapCompleted = []int32{-1, -1, -1}

	if got := formatPositionLine(ld); !strings.Contains(got, "Lap 1") {
		t.Errorf("formatPositionLine = %q, want Lap 1 before the first crossing", got)
	}
}

// With no resolved CarIdx there is no position to report, and printing 0 would
// claim the driver is leading.
func TestFormatPositionLine_UnresolvedCarIdx(t *testing.T) {
	ld := liveFixture()
	ld.MyCarIdx = -1

	got := formatPositionLine(ld)
	if !strings.Contains(got, "Pos ?") || !strings.Contains(got, "Lap ?") {
		t.Errorf("formatPositionLine = %q, want placeholders when CarIdx is unresolved", got)
	}
}

func TestFormatGap_NoCar(t *testing.T) {
	if got := formatGap(liveFixture(), iracing.NoGap); got != "(none)" {
		t.Errorf("formatGap(NoGap) = %q, want (none)", got)
	}
}

func TestFormatGap_AheadAndBehind(t *testing.T) {
	ld := liveFixture()

	ahead := formatGap(ld, iracing.GapTo{CarIdx: 0, TimeSeconds: 1.234})
	if !strings.Contains(ahead, "#11") || !strings.Contains(ahead, "Ahead Driver") {
		t.Errorf("formatGap ahead = %q", ahead)
	}
	if !strings.Contains(ahead, "+1.234s") {
		t.Errorf("formatGap ahead = %q, want +1.234s", ahead)
	}

	// A car behind has a negative time, rendered with an explicit minus sign
	// rather than a signed float, so the columns stay aligned.
	behind := formatGap(ld, iracing.GapTo{CarIdx: 2, TimeSeconds: -0.75})
	if !strings.Contains(behind, "-0.750s") {
		t.Errorf("formatGap behind = %q, want -0.750s", behind)
	}
}

func TestFormatGap_LappedCars(t *testing.T) {
	ld := liveFixture()

	up := formatGap(ld, iracing.GapTo{CarIdx: 0, TimeSeconds: 5, LapsDelta: 1})
	if !strings.Contains(up, "(+1 lap)") {
		t.Errorf("formatGap = %q, want a +1 lap note", up)
	}
	down := formatGap(ld, iracing.GapTo{CarIdx: 2, TimeSeconds: -5, LapsDelta: -1})
	if !strings.Contains(down, "(-1 lap)") {
		t.Errorf("formatGap = %q, want a -1 lap note", down)
	}
}

// An unknown CarIdx still has to render something; the gap itself is valid.
func TestFormatGap_UnknownDriver(t *testing.T) {
	ld := liveFixture()
	ld.Drivers = nil

	got := formatGap(ld, iracing.GapTo{CarIdx: 0, TimeSeconds: 1})
	if !strings.Contains(got, "#?") {
		t.Errorf("formatGap = %q, want a ? placeholder for the car number", got)
	}
}

func TestGapsFromLive(t *testing.T) {
	ahead, behind := gapsFromLive(liveFixture())

	if ahead.CarIdx != 0 {
		t.Errorf("ahead CarIdx = %d, want 0", ahead.CarIdx)
	}
	if behind.CarIdx != 2 {
		t.Errorf("behind CarIdx = %d, want 2", behind.CarIdx)
	}
	if ahead.TimeSeconds <= 0 {
		t.Errorf("car ahead should have a positive gap, got %v", ahead.TimeSeconds)
	}
	if behind.TimeSeconds >= 0 {
		t.Errorf("car behind should have a negative gap, got %v", behind.TimeSeconds)
	}
}

func TestGapsFromLive_UnresolvedCarIdx(t *testing.T) {
	ld := liveFixture()
	ld.MyCarIdx = -1

	ahead, behind := gapsFromLive(ld)
	if ahead.CarIdx > 0 || behind.CarIdx > 0 {
		t.Errorf("expected no gaps without a resolved CarIdx: %+v %+v", ahead, behind)
	}
}

// Solo practice: the player is the only car on track.
func TestGapsFromLive_SoloSession(t *testing.T) {
	ld := iracing.LiveData{
		Connected:          true,
		MyCarIdx:           0,
		CarIdxLapDistPct:   []float32{0.5, -1, -1},
		CarIdxLapCompleted: []int32{2, -1, -1},
		CarIdxEstTime:      []float32{50, -1, -1},
	}

	ahead, behind := gapsFromLive(ld)
	if ahead.CarIdx >= 0 || behind.CarIdx >= 0 {
		t.Errorf("solo session should report no gaps, got %+v %+v", ahead, behind)
	}
}

func TestPrintGapView_Connected(t *testing.T) {
	out := captureStdoutForTest(t, func() { printGapView(liveFixture()) })

	for _, want := range []string{"Watkins Glen", "Porsche 718 GT4", "Pos 2/3", "Ahead", "Behind"} {
		if !strings.Contains(out, want) {
			t.Errorf("gap view missing %q:\n%s", want, out)
		}
	}
}

func TestPrintGapView_NotConnected(t *testing.T) {
	out := captureStdoutForTest(t, func() {
		printGapView(iracing.LiveData{Connected: false})
	})
	if !strings.Contains(out, "not connected") {
		t.Errorf("expected a not-connected message:\n%s", out)
	}
}

// A specific error from the shared-memory read is more useful than the generic
// fallback, so it must be preferred when present.
func TestPrintGapView_PrefersSpecificError(t *testing.T) {
	out := captureStdoutForTest(t, func() {
		printGapView(iracing.LiveData{Connected: false, ErrMsg: "OpenFileMappingW failed"})
	})
	if !strings.Contains(out, "OpenFileMappingW failed") {
		t.Errorf("expected the specific error:\n%s", out)
	}
}

func TestPrintGapLine(t *testing.T) {
	out := captureStdoutForTest(t, func() { printGapLine(liveFixture()) })

	if !strings.Contains(out, "Ahead") || !strings.Contains(out, "Behind") {
		t.Errorf("gap line missing labels:\n%s", out)
	}
	// -watch prints one line per tick.
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("expected a single line, got:\n%s", out)
	}
}

func TestPrintGapLine_NotConnected(t *testing.T) {
	out := captureStdoutForTest(t, func() {
		printGapLine(iracing.LiveData{Connected: false})
	})
	if !strings.Contains(out, "[not connected]") {
		t.Errorf("expected a bracketed status:\n%s", out)
	}
}

func TestPrintSnapshotCompactAndVerbose(t *testing.T) {
	ld := liveFixture()

	compact := captureStdoutForTest(t, func() { printSnapshotCompact(ld) })
	if compact == "" {
		t.Error("compact snapshot produced no output")
	}

	verbose := captureStdoutForTest(t, func() { printSnapshotVerbose(ld) })
	if verbose == "" {
		t.Error("verbose snapshot produced no output")
	}
	// -raw exists to show per-car detail the formatted view hides.
	if len(verbose) <= len(compact) {
		t.Errorf("verbose snapshot should carry more detail than compact\ncompact:\n%s\nverbose:\n%s",
			compact, verbose)
	}
}
