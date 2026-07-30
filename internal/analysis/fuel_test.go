package analysis

import (
	"math"
	"testing"
)

// fuelLap builds a lap whose tank drops from start to end over its samples.
func fuelLap(number int, start, end float32, ratePerHour float32) Lap {
	const n = 60
	var samples []SampleData
	for i := 0; i < n; i++ {
		frac := float32(i) / float32(n-1)
		samples = append(samples, SampleData{
			LapDistPct:     float32(i) / float32(n),
			SessionTime:    float64(number*100) + float64(i)/60.0,
			Speed:          50,
			FuelLevel:      start + (end-start)*frac,
			FuelUsePerHour: ratePerHour,
		})
	}
	return Lap{Number: number, LapTime: 100, Kind: KindFlying, Samples: samples}
}

func TestComputeFuel_PerLapConsumption(t *testing.T) {
	laps := []Lap{
		fuelLap(1, 50, 48, 55),
		fuelLap(2, 48, 45.5, 55),
		fuelLap(3, 45.5, 43.2, 55),
	}

	f := ComputeFuel(laps, laps)
	if !f.Available {
		t.Fatal("expected fuel data to be available")
	}
	if f.LapsMeasured != 3 {
		t.Errorf("LapsMeasured = %d, want 3", f.LapsMeasured)
	}
	if math.Abs(float64(f.UsedLitres)-6.8) > 0.01 {
		t.Errorf("UsedLitres = %.3f, want ~6.8", f.UsedLitres)
	}
	// Laps burn 2.0, 2.5, 2.3.
	if math.Abs(float64(f.AvgPerLap)-2.2667) > 0.01 {
		t.Errorf("AvgPerLap = %.3f, want ~2.267", f.AvgPerLap)
	}
	if math.Abs(float64(f.WorstPerLap)-2.5) > 0.01 {
		t.Errorf("WorstPerLap = %.3f, want 2.5", f.WorstPerLap)
	}
	if math.Abs(float64(f.MedianPerLap)-2.3) > 0.01 {
		t.Errorf("MedianPerLap = %.3f, want 2.3", f.MedianPerLap)
	}
	if math.Abs(float64(f.AvgUsePerHour)-55) > 0.01 {
		t.Errorf("AvgUsePerHour = %.2f, want 55", f.AvgUsePerHour)
	}
}

// Planning on the average runs dry half the time, so both figures must be
// reported and the worst-lap one must be the more pessimistic.
func TestComputeFuel_LapsRemaining(t *testing.T) {
	laps := []Lap{
		fuelLap(1, 50, 48, 55),   // 2.0
		fuelLap(2, 48, 45.5, 55), // 2.5
	}

	f := ComputeFuel(laps, laps)
	// 45.5 remaining; avg 2.25/lap → 20.2 laps; worst 2.5/lap → 18.2 laps.
	if math.Abs(float64(f.LapsRemainingAvg)-20.22) > 0.05 {
		t.Errorf("LapsRemainingAvg = %.2f, want ~20.22", f.LapsRemainingAvg)
	}
	if math.Abs(float64(f.LapsRemainingWorst)-18.2) > 0.05 {
		t.Errorf("LapsRemainingWorst = %.2f, want ~18.2", f.LapsRemainingWorst)
	}
	if f.LapsRemainingWorst >= f.LapsRemainingAvg {
		t.Error("the worst-lap figure must be the more pessimistic of the two")
	}
}

// A session that ends with a pit stop has a full tank in its final sample.
// Reporting that as "remaining" would tell a driver who just ran a stint that
// they have a full tank — the exact case this logic exists for.
func TestComputeFuel_RefuelDoesNotCancelConsumption(t *testing.T) {
	laps := []Lap{
		fuelLap(1, 50, 47.8, 55),
		fuelLap(2, 47.8, 45.5, 55),
		fuelLap(3, 45.5, 50, 55), // pits and refuels back to full
	}

	f := ComputeFuel(laps, laps)
	if !f.Refuelled {
		t.Error("expected the refuel to be flagged")
	}
	// Raw endpoints would give 50-50 = 0 used and 50 L remaining.
	if math.Abs(float64(f.UsedLitres)-4.5) > 0.01 {
		t.Errorf("UsedLitres = %.3f, want ~4.5 (sum of real laps, not start-end)", f.UsedLitres)
	}
	if math.Abs(float64(f.EndLitres)-45.5) > 0.01 {
		t.Errorf("EndLitres = %.3f, want 45.5 (end of the last non-refuel lap)", f.EndLitres)
	}
	if f.LapsMeasured != 2 {
		t.Errorf("LapsMeasured = %d, want 2 — the refuel lap is not a measurement", f.LapsMeasured)
	}
	for _, lf := range f.PerLap {
		if lf.LapNumber == 3 && lf.UsedLitres != 0 {
			t.Errorf("refuelled lap reported %.2f L used; burn and fill can't be separated", lf.UsedLitres)
		}
	}
}

// Out laps cover less than a full lap of track and would understate
// consumption, so only the laps named as the measurement population count.
func TestComputeFuel_AveragesOnlyOverMeasureLaps(t *testing.T) {
	outLap := fuelLap(1, 50, 49.9, 55) // barely burns anything
	outLap.Kind = KindOutLap
	flying := []Lap{fuelLap(2, 49.9, 47.5, 55), fuelLap(3, 47.5, 45.2, 55)}
	all := append([]Lap{outLap}, flying...)

	f := ComputeFuel(all, flying)
	if f.LapsMeasured != 2 {
		t.Fatalf("LapsMeasured = %d, want 2", f.LapsMeasured)
	}
	if f.AvgPerLap < 2.0 {
		t.Errorf("AvgPerLap = %.3f — the out lap dragged the average down", f.AvgPerLap)
	}
	// The out lap still appears in the per-lap breakdown.
	if len(f.PerLap) != 3 {
		t.Errorf("PerLap has %d entries, want 3 (every lap is listed)", len(f.PerLap))
	}
}

// A car with no fuel telemetry presents as a flat zero channel; reporting that
// as a session that used no fuel would be worse than reporting nothing.
func TestComputeFuel_NoChannel(t *testing.T) {
	var laps []Lap
	for n := 1; n <= 3; n++ {
		laps = append(laps, fuelLap(n, 0, 0, 0))
	}
	if f := ComputeFuel(laps, laps); f.Available {
		t.Error("a flat/absent FuelLevel channel should not be reported as available")
	}
}

func TestComputeFuel_NoLaps(t *testing.T) {
	if f := ComputeFuel(nil, nil); f.Available {
		t.Error("expected unavailable with no laps")
	}
}

func TestFuelForLaps(t *testing.T) {
	// 2.5 L/lap for 10 laps plus one lap of margin = 27.5 L.
	if got := FuelForLaps(2.5, 10, 1); math.Abs(float64(got)-27.5) > 0.001 {
		t.Errorf("FuelForLaps(2.5, 10, 1) = %.3f, want 27.5", got)
	}
	if got := FuelForLaps(2.5, 10, 0); math.Abs(float64(got)-25) > 0.001 {
		t.Errorf("FuelForLaps with no margin = %.3f, want 25", got)
	}
	// Nonsense inputs yield 0 rather than a negative or NaN plan.
	if got := FuelForLaps(0, 10, 1); got != 0 {
		t.Errorf("FuelForLaps with no rate = %.3f, want 0", got)
	}
	if got := FuelForLaps(2.5, 0, 1); got != 0 {
		t.Errorf("FuelForLaps for 0 laps = %.3f, want 0", got)
	}
}
