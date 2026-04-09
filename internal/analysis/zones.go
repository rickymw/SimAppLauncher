package analysis

import (
	"math"

	"github.com/rickymw/MotorHome/internal/trackmap"
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

	// tcCutThreshold is the minimum ThrottleRaw−Throttle difference that
	// counts as active traction control intervention.
	tcCutThreshold = float32(0.02)

	// lockupRatio: a wheel is locking if its speed < this fraction of vehicle speed.
	lockupRatio = float32(0.95)

	// wheelspinRatio: a wheel is spinning if its speed > this fraction of vehicle speed.
	wheelspinRatio = float32(1.05)
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
	LatGAvg       float32 // average lateral G (absolute value)
	LongDecelMax  float32 // peak deceleration G (0 during acceleration)
	ABSCount      int     // samples where ABS was active
	CoastSamples  int     // samples with throttle < 5% AND brake < 5%
	SampleCount   int     // total samples bucketed into this zone

	// TC intervention: ThrottleRaw − Throttle when TC is cutting power.
	TCInterventionPct float32 // % of samples where TC is active (cut > 2%)
	PeakTCCut         float32 // max (ThrottleRaw − Throttle) × 100

	// Wheel lockup/spin detection.
	LockupSamples   int // samples where any wheel speed < 95% of vehicle speed under braking
	WheelspinSamples int // samples where any wheel speed > 105% of vehicle speed under power
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
	tcActiveCounts := make([]int, NumZones)

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

			z.LatGAvg += abs32(s.LatAccel) / grav
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

			// TC intervention: ThrottleRaw − Throttle > tcCutThreshold.
			tcCut := s.ThrottleRaw - s.Throttle
			if tcCut > tcCutThreshold {
				tcActiveCounts[i]++
				if tcCut*100 > z.PeakTCCut {
					z.PeakTCCut = tcCut * 100
				}
			}

			// Lockup: any wheel speed < 95% of vehicle speed while braking.
			if s.Brake > brakeOnThreshold && s.Speed > 5 {
				minWheel := min32(s.LFspeed, s.RFspeed, s.LRspeed, s.RRspeed)
				if minWheel < s.Speed*lockupRatio {
					z.LockupSamples++
				}
			}

			// Wheelspin: any wheel speed > 105% of vehicle speed under power.
			if s.Throttle > 0.5 && s.Speed > 5 {
				maxWheel := max32(s.LFspeed, s.RFspeed, s.LRspeed, s.RRspeed)
				if maxWheel > s.Speed*wheelspinRatio {
					z.WheelspinSamples++
				}
			}

			gearCounts[s.Gear]++
		}

		z.SpeedMinKPH = minSpd

		n := float32(len(samples))
		z.BrakePct = 100 * float32(brakeOnCounts[i]) / n
		z.ThrottlePct = 100 * float32(thrFullCounts[i]) / n
		z.LatGAvg /= n
		z.TCInterventionPct = 100 * float32(tcActiveCounts[i]) / n

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

// min32 returns the minimum of the given float32 values.
func min32(vals ...float32) float32 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// max32 returns the maximum of the given float32 values.
func max32(vals ...float32) float32 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
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
	LatGAvg       float32 // average abs(LatAccel)/9.81
	ABSCount      int     // samples where ABS was active
	CoastSamples  int     // samples with throttle<5% AND brake<5%
	SampleCount   int     // total samples in the segment

	// TC intervention
	TCInterventionPct float32 // % of samples where TC is active (ThrottleRaw−Throttle > 2%)
	PeakTCCut         float32 // max (ThrottleRaw − Throttle) × 100

	// Wheel lockup/spin
	LockupSamples    int // samples where any wheel speed < 95% of vehicle speed under braking
	WheelspinSamples int // samples where any wheel speed > 105% of vehicle speed under power

	// Tyre temps — average carcass middle temp per corner (°C)
	AvgTyreTempLF float32
	AvgTyreTempRF float32
	AvgTyreTempLR float32
	AvgTyreTempRR float32
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

		// How far back to look: the preceding segment's effective entry (uses
		// BrakeEntryPct for corners, geometric EntryPct for straights). This
		// prevents the scan from crossing into an adjacent corner's braking zone.
		var minPct float32
		if i > 0 {
			minPct = effectiveSegEntry(segs[i-1])
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
			// A tolerance of 3 consecutive non-braking samples is allowed so
			// that brief ABS modulation or trail-braking dips do not terminate
			// the scan prematurely.
			const brakeReleaseTolerance = 3
			onset := seg.EntryPct
			inBraking := false
			releaseCount := 0
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
					releaseCount = 0
					onset = s.LapDistPct // extend onset further back
				} else if inBraking {
					releaseCount++
					if releaseCount > brakeReleaseTolerance {
						break // found the trailing edge of the braking zone
					}
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
	latGSums := make([]float32, len(segs))
	brakeOnCounts := make([]int, len(segs))
	thrFullCounts := make([]int, len(segs))
	tcActiveCounts := make([]int, len(segs))
	tyreTempSums := make([][4]float32, len(segs)) // LF, RF, LR, RR mid-carcass sums
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
		latGSums[idx] += abs32(s.LatAccel) / grav
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

		// TC intervention.
		tcCut := s.ThrottleRaw - s.Throttle
		if tcCut > tcCutThreshold {
			tcActiveCounts[idx]++
			if tcCut*100 > z.PeakTCCut {
				z.PeakTCCut = tcCut * 100
			}
		}

		// Lockup detection.
		if s.Brake > brakeOnThreshold && s.Speed > 5 {
			minWheel := min32(s.LFspeed, s.RFspeed, s.LRspeed, s.RRspeed)
			if minWheel < s.Speed*lockupRatio {
				z.LockupSamples++
			}
		}

		// Wheelspin detection.
		if s.Throttle > 0.5 && s.Speed > 5 {
			maxWheel := max32(s.LFspeed, s.RFspeed, s.LRspeed, s.RRspeed)
			if maxWheel > s.Speed*wheelspinRatio {
				z.WheelspinSamples++
			}
		}

		// Tyre temps (mid-carcass).
		tyreTempSums[idx][0] += s.LFtempCM
		tyreTempSums[idx][1] += s.RFtempCM
		tyreTempSums[idx][2] += s.LRtempCM
		tyreTempSums[idx][3] += s.RRtempCM

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
		zones[idx].LatGAvg = latGSums[idx] / n
		zones[idx].TCInterventionPct = 100 * float32(tcActiveCounts[idx]) / n

		// Average tyre temps.
		zones[idx].AvgTyreTempLF = tyreTempSums[idx][0] / n
		zones[idx].AvgTyreTempRF = tyreTempSums[idx][1] / n
		zones[idx].AvgTyreTempLR = tyreTempSums[idx][2] / n
		zones[idx].AvgTyreTempRR = tyreTempSums[idx][3] / n

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

