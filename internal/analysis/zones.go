package analysis

import "math"

const (
	// NumZones is the number of equal-distance track sections per lap.
	NumZones = 20 // each zone = 5% of LapDistPct

	ms2kmh           = 3.6      // m/s → km/h
	grav     float32 = 9.81     // m/s² per g
)

// Zone holds computed statistics for one 5%-of-track section.
// Speed fields are in km/h; input fields are 0–100%; G-force fields are in g.
type Zone struct {
	Index         int
	SpeedEntryKPH float32 // speed of the first sample in the zone
	SpeedMinKPH   float32 // minimum speed seen in the zone (apex speed)
	SpeedExitKPH  float32 // speed of the last sample in the zone
	BrakePct      float32 // peak brake pressure 0–100%
	ThrottlePct   float32 // peak throttle 0–100%
	DominantGear  int32   // modal gear (most common; neutral/reverse excluded if possible)
	LatGMax       float32 // peak lateral G (absolute value)
	LongDecelMax  float32 // peak deceleration G (0 during acceleration)
	ABSCount      int     // samples where ABS was active
	CoastSamples  int     // samples with throttle < 5% AND brake < 5%
	SampleCount   int     // total samples bucketed into this zone
}

// ZoneStats computes per-zone statistics for a single lap.
// Returns exactly NumZones entries, one per 5% track distance segment.
func ZoneStats(lap *Lap) []Zone {
	// Bucket samples by zone index.
	buckets := make([][]SampleData, NumZones)
	for _, s := range lap.Samples {
		buckets[zoneIdx(s.LapDistPct)] = append(buckets[zoneIdx(s.LapDistPct)], s)
	}

	zones := make([]Zone, NumZones)
	for i, samples := range buckets {
		z := &zones[i]
		z.Index = i
		z.SampleCount = len(samples)

		if len(samples) == 0 {
			continue
		}

		z.SpeedEntryKPH = samples[0].Speed * ms2kmh
		z.SpeedExitKPH = samples[len(samples)-1].Speed * ms2kmh

		minSpd := float32(math.MaxFloat32)
		gearCounts := map[int32]int{}

		for _, s := range samples {
			spd := s.Speed * ms2kmh
			if spd < minSpd {
				minSpd = spd
			}

			brkPct := s.Brake * 100
			if brkPct > z.BrakePct {
				z.BrakePct = brkPct
			}
			thrPct := s.Throttle * 100
			if thrPct > z.ThrottlePct {
				z.ThrottlePct = thrPct
			}

			latG := abs32(s.LatAccel) / grav
			if latG > z.LatGMax {
				z.LatGMax = latG
			}
			// LongAccel is positive for forward acceleration, negative under braking.
			// LongDecelMax tracks peak deceleration (positive g value).
			decel := max(-s.LongAccel/grav, float32(0))
			if decel > z.LongDecelMax {
				z.LongDecelMax = decel
			}

			if s.ABSActive {
				z.ABSCount++
			}
			if s.Throttle < 0.05 && s.Brake < 0.05 {
				z.CoastSamples++
			}

			gearCounts[s.Gear]++
		}

		if minSpd < math.MaxFloat32 {
			z.SpeedMinKPH = minSpd
		}

		// Dominant gear: modal value, preferring forward gears (>0) over neutral/reverse.
		bestGear, bestCount := int32(0), 0
		for g, c := range gearCounts {
			if g > 0 && c > bestCount {
				bestGear, bestCount = g, c
			}
		}
		if bestCount == 0 {
			// All samples are in neutral or reverse — use raw mode.
			for g, c := range gearCounts {
				if c > bestCount {
					bestGear, bestCount = g, c
				}
			}
		}
		z.DominantGear = bestGear
	}

	return zones
}

// ZoneDeltas computes the per-zone time contribution of the delta between
// two laps. A negative value means lap2 was faster through that zone;
// a positive value means lap1 was faster.
//
// The algorithm:
//  1. Compute the lap time reached at each of 21 zone boundaries (0%, 5%, …, 100%)
//     for both laps using linear interpolation of LapCurrentLapTime vs LapDistPct.
//  2. The time each lap spent in zone z = cumulative[z+1] − cumulative[z].
//  3. Delta[z] = (lap2 time in zone z) − (lap1 time in zone z).
func ZoneDeltas(lap1, lap2 *Lap) []float32 {
	deltas := make([]float32, NumZones)
	if len(lap1.Samples) == 0 || len(lap2.Samples) == 0 {
		return deltas
	}

	var cum1, cum2 [NumZones + 1]float32
	for k := 0; k <= NumZones; k++ {
		pct := float32(k) / float32(NumZones)
		cum1[k] = timeAtPct(lap1, pct)
		cum2[k] = timeAtPct(lap2, pct)
	}

	for z := 0; z < NumZones; z++ {
		zoneTime1 := cum1[z+1] - cum1[z]
		zoneTime2 := cum2[z+1] - cum2[z]
		deltas[z] = zoneTime2 - zoneTime1
	}

	return deltas
}

// timeAtPct returns the elapsed time since lap start (in seconds) when
// LapDistPct reaches targetPct, using linear interpolation of SessionTime.
// SessionTime is used because iRacing's LapCurrentLapTime does not reset
// at the S/F line; SessionTime is a monotonically increasing, reliable clock.
func timeAtPct(lap *Lap, targetPct float32) float32 {
	if len(lap.Samples) == 0 {
		return 0
	}

	elapsedAt := func(s SampleData) float32 {
		return float32(s.SessionTime - lap.StartSessionTime)
	}

	first := lap.Samples[0]
	if targetPct <= first.LapDistPct {
		return elapsedAt(first)
	}
	for i := 1; i < len(lap.Samples); i++ {
		prev := lap.Samples[i-1]
		curr := lap.Samples[i]
		if prev.LapDistPct <= targetPct && curr.LapDistPct >= targetPct {
			span := curr.LapDistPct - prev.LapDistPct
			if span < 1e-6 {
				return elapsedAt(prev)
			}
			frac := (targetPct - prev.LapDistPct) / span
			return elapsedAt(prev) + frac*(elapsedAt(curr)-elapsedAt(prev))
		}
	}
	// targetPct is beyond the last sample.
	return elapsedAt(lap.Samples[len(lap.Samples)-1])
}

// zoneIdx maps a LapDistPct value (0.0–1.0) to a zone index (0–NumZones-1).
func zoneIdx(pct float32) int {
	zi := int(pct * NumZones)
	if zi < 0 {
		return 0
	}
	if zi >= NumZones {
		return NumZones - 1
	}
	return zi
}

// abs32 returns the absolute value of a float32.
func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
