package analysis

import (
	"math"

	"github.com/rickymw/SimAppLauncher/internal/trackmap"
)

const (
	// NumZones is the number of equal-distance track sections per lap.
	NumZones = 20 // each zone = 5% of LapDistPct

	ms2kmh           = 3.6      // m/s → km/h
	grav     float32 = 9.81     // m/s² per g

	// Input thresholds used for BrakePct / ThrottlePct fraction computation.
	brakeOnThreshold   = float32(0.02) // brake pressure > 2% counts as "on brakes"
	fullThrottleThresh = float32(0.95) // throttle > 95% counts as "full throttle"

	// brakeEntryThreshold is the minimum brake pressure (0.0–1.0) that marks
	// the start of a braking zone when scanning backward from a corner entry.
	brakeEntryThreshold = float32(0.05)
)

// Zone holds computed statistics for one 5%-of-track section.
// Speed fields are in km/h; input fields are 0–100%; G-force fields are in g.
type Zone struct {
	Index         int
	SpeedEntryKPH float32 // speed of the first sample in the zone
	SpeedMinKPH   float32 // minimum speed seen in the zone (apex speed)
	SpeedExitKPH  float32 // speed of the last sample in the zone
	BrakePct      float32 // % of samples with brake pressure > 2% (time on brakes)
	ThrottlePct   float32 // % of samples at full throttle (> 95%)
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
		zi := zoneIdx(s.LapDistPct)
		buckets[zi] = append(buckets[zi], s)
	}

	zones := make([]Zone, NumZones)
	brakeOnCounts := make([]int, NumZones)
	thrFullCounts := make([]int, NumZones)

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

			if s.Brake > brakeOnThreshold {
				brakeOnCounts[i]++
			}
			if s.Throttle > fullThrottleThresh {
				thrFullCounts[i]++
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

		n := float32(len(samples))
		z.BrakePct = 100 * float32(brakeOnCounts[i]) / n
		z.ThrottlePct = 100 * float32(thrFullCounts[i]) / n

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

// SegZone holds computed statistics for one geometry-based track segment.
// Speed fields are in km/h; input fields are 0–100%; G-force fields are in g.
type SegZone struct {
	Name          string
	Kind          trackmap.SegmentKind
	EntryPct      float32
	ExitPct       float32
	SpeedEntryKPH float32 // speed of the first sample in the segment
	SpeedMinKPH   float32 // minimum speed in the segment (apex speed)
	SpeedExitKPH  float32 // speed of the last sample in the segment
	BrakePct      float32 // % of samples with brake pressure > 2% (time on brakes)
	PeakBrakePct  float32 // maximum brake pressure seen in the segment (0–100%)
	ThrottlePct   float32 // % of samples at full throttle (> 95%)
	DominantGear  int32   // max forward gear for straights; min forward gear for corners/chicanes
	LatGMax       float32 // peak abs(LatAccel)/9.81
	ABSCount      int     // samples where ABS was active
	CoastSamples  int     // samples with throttle<5% AND brake<5%
	SampleCount   int     // total samples in the segment
}

// effectiveSegEntry returns the effective entry percentage for a segment.
// For corners and chicanes with a computed BrakeEntryPct, that value is used;
// otherwise the geometric EntryPct is returned.
func effectiveSegEntry(seg trackmap.Segment) float32 {
	if seg.Kind != trackmap.KindStraight && seg.BrakeEntryPct > 0 {
		return seg.BrakeEntryPct
	}
	return seg.EntryPct
}

// ComputeBrakeEntries scans flying laps to find the average braking onset
// point before each corner/chicane segment. For each such segment it scans
// backward from the geometric corner entry, looking for the start of the
// contiguous braking zone (Brake > brakeEntryThreshold = 5%). The result is
// averaged across all flying non-partial-start laps.
//
// Returns a []float32 of effective entry percentages, one per segment. Straights
// and corners where no braking was detected keep the geometric EntryPct.
func ComputeBrakeEntries(laps []Lap, segs []trackmap.Segment) []float32 {
	entries := make([]float32, len(segs))
	for i, seg := range segs {
		entries[i] = seg.EntryPct
	}

	for i, seg := range segs {
		if seg.Kind == trackmap.KindStraight {
			continue
		}

		// How far back to look: the preceding segment's geometric entry.
		var minPct float32
		if i > 0 {
			minPct = segs[i-1].EntryPct
		}

		var totalOnset float32
		var lapCount int

		for k := range laps {
			lap := &laps[k]
			if lap.Kind != KindFlying || lap.IsPartialStart {
				continue
			}

			// Scan backward from the geometric corner entry to find the start
			// of the contiguous braking zone immediately before it.
			onset := seg.EntryPct
			inBraking := false
			for j := len(lap.Samples) - 1; j >= 0; j-- {
				s := lap.Samples[j]
				if s.LapDistPct >= seg.EntryPct {
					continue // still in/after the corner
				}
				if s.LapDistPct < minPct {
					break // past the start of the preceding segment
				}
				if s.Brake > brakeEntryThreshold {
					inBraking = true
					onset = s.LapDistPct // extend onset further back
				} else if inBraking {
					break // found the trailing edge of the braking zone
				}
			}

			totalOnset += onset
			lapCount++
		}

		if lapCount > 0 {
			entries[i] = totalOnset / float32(lapCount)
		}
	}

	return entries
}

// segmentForEffPct returns the segment index for a sample at pct, using
// pre-computed effective entry and exit boundaries. Returns -1 if no match.
// The last segment absorbs any pct >= its effective entry (handles pct=1.0).
func segmentForEffPct(pct float32, effEntry, effExit []float32) int {
	last := len(effEntry) - 1
	for i := range effEntry {
		if i == last {
			if pct >= effEntry[i] {
				return i
			}
		} else if pct >= effEntry[i] && pct < effExit[i] {
			return i
		}
	}
	return -1
}

// SegmentStats computes per-segment statistics for a single lap.
// Returns one SegZone for each entry in segs.
//
// Sample assignment uses effective boundaries: for corners/chicanes with a
// stored BrakeEntryPct, that value is used as the segment entry instead of the
// geometric EntryPct. Each preceding straight's effective exit is clipped to the
// next corner's BrakeEntryPct so that braking-zone samples are attributed to the
// corner rather than the straight. The EntryPct/ExitPct fields of the returned
// SegZone reflect these effective boundaries.
func SegmentStats(lap *Lap, segs []trackmap.Segment) []SegZone {
	if len(segs) == 0 {
		return nil
	}

	// Pre-compute effective entry and exit for each segment.
	effEntry := make([]float32, len(segs))
	effExit := make([]float32, len(segs))
	for i, seg := range segs {
		effEntry[i] = effectiveSegEntry(seg)
		effExit[i] = seg.ExitPct
	}
	// Clip each segment's exit to the next segment's effective entry so there
	// are no overlaps and braking-zone samples fall into the corner.
	for i := 0; i < len(segs)-1; i++ {
		if effEntry[i+1] < effExit[i] {
			effExit[i] = effEntry[i+1]
		}
	}

	zones := make([]SegZone, len(segs))
	for i, seg := range segs {
		zones[i].Name = seg.Name
		zones[i].Kind = seg.Kind
		zones[i].EntryPct = effEntry[i] // effective boundary for display
		zones[i].ExitPct = effExit[i]
	}

	minSpeeds := make([]float32, len(segs))
	brakeOnCounts := make([]int, len(segs))
	thrFullCounts := make([]int, len(segs))
	// minGear / maxGear track the lowest and highest forward gear (≥1) per segment.
	minGears := make([]int32, len(segs))
	maxGears := make([]int32, len(segs))
	for i := range minSpeeds {
		minSpeeds[i] = float32(math.MaxFloat32)
		minGears[i] = math.MaxInt32
	}

	for _, s := range lap.Samples {
		idx := segmentForEffPct(s.LapDistPct, effEntry, effExit)
		if idx < 0 {
			continue
		}

		z := &zones[idx]
		if z.SampleCount == 0 {
			z.SpeedEntryKPH = s.Speed * ms2kmh
		}
		z.SpeedExitKPH = s.Speed * ms2kmh

		spd := s.Speed * ms2kmh
		if spd < minSpeeds[idx] {
			minSpeeds[idx] = spd
		}

		if s.Brake > brakeOnThreshold {
			brakeOnCounts[idx]++
		}
		if brkPct := s.Brake * 100; brkPct > zones[idx].PeakBrakePct {
			zones[idx].PeakBrakePct = brkPct
		}
		if s.Throttle > fullThrottleThresh {
			thrFullCounts[idx]++
		}
		if latG := abs32(s.LatAccel) / grav; latG > z.LatGMax {
			z.LatGMax = latG
		}
		if s.ABSActive {
			z.ABSCount++
		}
		if s.Throttle < 0.05 && s.Brake < 0.05 {
			z.CoastSamples++
		}

		// Track min/max forward gear for this segment.
		if s.Gear >= 1 {
			if s.Gear < minGears[idx] {
				minGears[idx] = s.Gear
			}
			if s.Gear > maxGears[idx] {
				maxGears[idx] = s.Gear
			}
		}

		z.SampleCount++
	}

	// Finalise per-segment values that require the full sample set.
	for idx := range zones {
		if zones[idx].SampleCount == 0 {
			continue // leave SpeedMinKPH as zero — caller checks SampleCount
		}
		zones[idx].SpeedMinKPH = minSpeeds[idx]

		n := float32(zones[idx].SampleCount)
		zones[idx].BrakePct = 100 * float32(brakeOnCounts[idx]) / n
		zones[idx].ThrottlePct = 100 * float32(thrFullCounts[idx]) / n

		// Gear selection depends on segment kind:
		//   Straight → highest gear reached (max speed gear)
		//   Corner / Chicane → lowest gear reached (apex gear)
		// If no forward-gear samples were seen, report 0.
		var gear int32
		if minGears[idx] == math.MaxInt32 {
			gear = 0 // no forward-gear samples
		} else if segs[idx].Kind == trackmap.KindStraight {
			gear = maxGears[idx]
		} else {
			gear = minGears[idx]
		}
		zones[idx].DominantGear = gear
	}

	return zones
}

// SegmentDeltas computes the per-segment time delta between two laps.
// A negative value means lap2 was faster through that segment.
// Returns one delta per segment (time of lap2 at segment entry minus time of lap1).
func SegmentDeltas(lap1, lap2 *Lap, segs []trackmap.Segment) []float32 {
	deltas := make([]float32, len(segs))
	if len(lap1.Samples) == 0 || len(lap2.Samples) == 0 || len(segs) == 0 {
		return deltas
	}

	// Build boundary pct values using effective entries so that the time delta
	// for each segment matches the sample attribution in SegmentStats.
	boundaries := make([]float32, len(segs)+1)
	for i, seg := range segs {
		boundaries[i] = effectiveSegEntry(seg)
	}
	boundaries[len(segs)] = 1.0

	cum1 := make([]float32, len(segs)+1)
	cum2 := make([]float32, len(segs)+1)
	for k, pct := range boundaries {
		cum1[k] = timeAtPct(lap1, pct)
		cum2[k] = timeAtPct(lap2, pct)
	}

	for z := 0; z < len(segs); z++ {
		zoneTime1 := cum1[z+1] - cum1[z]
		zoneTime2 := cum2[z+1] - cum2[z]
		deltas[z] = zoneTime2 - zoneTime1
	}

	return deltas
}
