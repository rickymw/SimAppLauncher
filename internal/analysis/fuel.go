package analysis

// Fuel consumption and stint planning from the FuelLevel channel.
//
// Consumption is derived from the tank level at each lap's boundaries rather
// than from FuelUsePerHour. The rate channel is an instantaneous reading and
// integrating it accumulates error across a lap; the difference between two
// tank readings is what actually went into the engine.

import "sort"

// fuelRefuelThreshold is the litre increase between consecutive samples of a
// lap's boundaries above which the lap is treated as containing a refuel
// rather than as sensor noise.
const fuelRefuelThreshold float32 = 0.5

// minPlausibleFuelPerLap is the floor below which a lap's computed consumption
// is treated as not measured. A real racing lap always burns something; a
// value at or below this means the channel is absent, flat, or the lap was too
// short to register.
const minPlausibleFuelPerLap float32 = 0.01

// LapFuel is one lap's fuel consumption.
type LapFuel struct {
	LapNumber   int
	StartLitres float32
	EndLitres   float32
	UsedLitres  float32
	// Refuelled is true when the tank gained fuel over the lap — a pit stop.
	// Such a lap's UsedLitres is not a consumption measurement and is excluded
	// from the averages.
	Refuelled bool
}

// FuelSummary describes fuel use across a session and what it implies for how
// much longer the car can run.
type FuelSummary struct {
	// Available is false when the .ibt has no usable FuelLevel channel, in
	// which case every other field is zero and must not be rendered.
	Available bool

	StartLitres float32 // tank level at the first sample of the session

	// EndLitres is the level at the end of the last lap that did not refuel,
	// not the last sample of the session. Sessions routinely end with a pit
	// stop that refills the tank, and reporting that as "remaining" would say
	// a car that just ran a stint has a full tank.
	EndLitres float32

	// Refuelled is true when any lap gained fuel, which is what makes
	// EndLitres and UsedLitres differ from the raw session endpoints.
	Refuelled bool

	// UsedLitres is the sum of per-lap consumption rather than
	// StartLitres-EndLitres, so a mid-session refuel doesn't cancel out the
	// fuel that was actually burned.
	UsedLitres float32

	PerLap []LapFuel // every lap, including out/in and refuelled laps

	// Averages below are computed only over the laps passed in as the
	// consumption population (normally flying laps) that did not refuel.
	LapsMeasured  int
	AvgPerLap     float32
	MedianPerLap  float32
	WorstPerLap   float32
	AvgUsePerHour float32 // mean FuelUsePerHour over the measured laps (kg/h)

	// LapsRemaining is EndLitres divided by the per-lap rate. Two figures
	// because planning on the average leaves you short half the time: the
	// worst-lap rate is what a stint has to survive.
	LapsRemainingAvg   float32
	LapsRemainingWorst float32
}

// ComputeFuel derives fuel consumption for every lap in laps, and averages over
// the subset named by measureLaps (pass the flying/comparable laps — an out lap
// covers less than a full lap of track and would understate consumption).
//
// Returns an unavailable summary when the FuelLevel channel is missing or flat,
// which is how a car with no fuel telemetry presents.
func ComputeFuel(laps []Lap, measureLaps []Lap) FuelSummary {
	var out FuelSummary
	if len(laps) == 0 {
		return out
	}

	measured := map[int]bool{}
	for _, l := range measureLaps {
		measured[l.Number] = true
	}

	var consumption []float32
	var rateSum float64
	var rateCount int

	for i := range laps {
		l := &laps[i]
		if len(l.Samples) == 0 {
			continue
		}
		start := l.Samples[0].FuelLevel
		end := l.Samples[len(l.Samples)-1].FuelLevel

		lf := LapFuel{
			LapNumber:   l.Number,
			StartLitres: start,
			EndLitres:   end,
			UsedLitres:  start - end,
		}
		// A tank that gained fuel means a pit stop, not negative consumption.
		if end-start > fuelRefuelThreshold {
			lf.Refuelled = true
			lf.UsedLitres = 0
		}
		out.PerLap = append(out.PerLap, lf)

		if !measured[l.Number] || lf.Refuelled || lf.UsedLitres < minPlausibleFuelPerLap {
			continue
		}
		consumption = append(consumption, lf.UsedLitres)

		for _, s := range l.Samples {
			if s.FuelUsePerHour > 0 {
				rateSum += float64(s.FuelUsePerHour)
				rateCount++
			}
		}
	}

	// Start is simply the first sample that exists.
	for i := range laps {
		if len(laps[i].Samples) > 0 {
			out.StartLitres = laps[i].Samples[0].FuelLevel
			break
		}
	}

	// End is the last lap that did not refuel, and total use is the sum of
	// per-lap consumption. Both avoid the session-endpoint trap: a session that
	// finishes with a pit stop has a full tank in its final sample, which would
	// otherwise report as "nothing used, tank full" after a completed stint.
	for i := len(out.PerLap) - 1; i >= 0; i-- {
		if !out.PerLap[i].Refuelled {
			out.EndLitres = out.PerLap[i].EndLitres
			break
		}
	}
	for _, lf := range out.PerLap {
		if lf.Refuelled {
			out.Refuelled = true
			continue
		}
		out.UsedLitres += lf.UsedLitres
	}

	if len(consumption) == 0 {
		// No lap yielded a usable figure — treat fuel as unreported rather
		// than reporting a tank that never moved.
		return out
	}

	out.Available = true
	out.LapsMeasured = len(consumption)

	var sum float32
	for _, v := range consumption {
		sum += v
		if v > out.WorstPerLap {
			out.WorstPerLap = v
		}
	}
	out.AvgPerLap = sum / float32(len(consumption))

	sorted := make([]float32, len(consumption))
	copy(sorted, consumption)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	out.MedianPerLap = sorted[len(sorted)/2]

	if rateCount > 0 {
		out.AvgUsePerHour = float32(rateSum / float64(rateCount))
	}

	if out.AvgPerLap > 0 {
		out.LapsRemainingAvg = out.EndLitres / out.AvgPerLap
	}
	if out.WorstPerLap > 0 {
		out.LapsRemainingWorst = out.EndLitres / out.WorstPerLap
	}

	return out
}

// FuelForLaps returns the litres needed to complete n laps at perLap, plus a
// margin expressed in laps.
//
// The margin is in laps rather than a percentage because that is how the
// decision is actually made in the pits — "a lap in hand" is a concrete amount
// of running, where "5%" is not.
func FuelForLaps(perLap float32, n int, marginLaps float32) float32 {
	if perLap <= 0 || n <= 0 {
		return 0
	}
	return perLap * (float32(n) + marginLaps)
}
