package analysis

import (
	"math"

	"github.com/rickymw/MotorHome/internal/pb"
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

// effectiveSegEntry returns the effective entry percentage for a segment.
// For corners and chicanes, the stored brake onset from brakeEntries is used
// if available; otherwise the geometric EntryPct is returned.
func effectiveSegEntry(seg trackmap.Segment, brakeEntries pb.BrakeEntryMap) float32 {
	if seg.Kind != trackmap.KindStraight {
		if entry, ok := brakeEntries[seg.Name]; ok && entry.Pct > 0 {
			return entry.Pct
		}
	}
	return seg.EntryPct
}

// EffectiveEntries builds a []float32 of effective entry percentages from
// brakeEntries, one per segment. Straights and corners without a stored brake
// onset keep the geometric EntryPct. This is a convenience helper for callers
// (e.g. ComputePhases) that need a positional slice rather than a map lookup.
func EffectiveEntries(segs []trackmap.Segment, brakeEntries pb.BrakeEntryMap) []float32 {
	entries := make([]float32, len(segs))
	for i, seg := range segs {
		entries[i] = effectiveSegEntry(seg, brakeEntries)
	}
	return entries
}

// ComputeBrakeEntries scans flying laps to find the average braking onset
// point before each corner/chicane segment. For each such segment it scans
// backward from the geometric corner entry, looking for the start of the
// contiguous braking zone (Brake > brakeEntryThreshold = 5%). The result is
// averaged across all flying non-partial-start laps.
//
// Returns a BrakeEntryMap keyed by segment name. Straights are omitted.
// Corners where no braking was detected are also omitted.
func ComputeBrakeEntries(laps []Lap, segs []trackmap.Segment) pb.BrakeEntryMap {
	result := make(pb.BrakeEntryMap)

	// Build a working slice of effective entry pcts (start with geometric values).
	// As we compute brake onset for each segment, we update this slice so that
	// earlier computed onset positions bound the scan for later segments.
	effEntry := make([]float32, len(segs))
	for i, seg := range segs {
		effEntry[i] = seg.EntryPct
	}

	for i, seg := range segs {
		if seg.Kind == trackmap.KindStraight {
			continue
		}

		// How far back to look: the preceding segment's effective entry. This
		// prevents the scan from crossing into an adjacent corner's braking zone.
		// For the first segment (i==0), the preceding segment wraps around to the
		// last segment, so minPct uses the last segment's entry.
		var minPct float32
		if i > 0 {
			minPct = effEntry[i-1]
		} else {
			minPct = effEntry[len(segs)-1]
		}

		// wrapAround is true when the search region crosses the S/F line
		// (i.e., the first corner's braking zone may start at pct > minPct near 1.0).
		wrapAround := i == 0 && minPct > seg.EntryPct

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

			// scanSample checks one sample for brake onset. Returns true to stop scanning.
			scanSample := func(s SampleData) bool {
				if s.Brake > brakeEntryThreshold {
					inBraking = true
					releaseCount = 0
					onset = s.LapDistPct
				} else if inBraking {
					releaseCount++
					if releaseCount > brakeReleaseTolerance {
						return true
					}
				}
				return false
			}

			if wrapAround {
				// First corner with braking before S/F: scan backward from the
				// corner entry (low pct), then wrap to the end of the lap (high pct).
				for j := len(lap.Samples) - 1; j >= 0; j-- {
					s := lap.Samples[j]
					if s.LapDistPct >= seg.EntryPct {
						continue // still in/after the corner
					}
					// In the low-pct region — scan until pct=0 (start of lap).
					if scanSample(s) {
						break
					}
				}
				if !inBraking || releaseCount <= brakeReleaseTolerance {
					// Continue scanning from the end of the lap (high-pct region).
					for j := len(lap.Samples) - 1; j >= 0; j-- {
						s := lap.Samples[j]
						if s.LapDistPct < minPct {
							break // past the preceding segment
						}
						if s.LapDistPct < seg.EntryPct {
							continue // already scanned the low-pct region
						}
						if scanSample(s) {
							break
						}
					}
				}
			} else {
				for j := len(lap.Samples) - 1; j >= 0; j-- {
					s := lap.Samples[j]
					if s.LapDistPct >= seg.EntryPct {
						continue // still in/after the corner
					}
					if s.LapDistPct < minPct {
						break // past the start of the preceding segment
					}
					if scanSample(s) {
						break
					}
				}
			}

			totalOnset += onset
			lapCount++
		}

		if lapCount > 0 {
			avg := totalOnset / float32(lapCount)
			// Only store when actual braking was detected earlier than the geometric
			// corner entry — if onset equals EntryPct the driver didn't brake before
			// this corner on these laps and no offset is useful.
			// For wrap-around (first corner), the onset pct will be > entryPct (near 1.0).
			if wrapAround {
				if avg != seg.EntryPct {
					result[seg.Name] = pb.BrakeEntry{Pct: avg, LapsUsed: lapCount}
					effEntry[i] = avg
				}
			} else if avg < seg.EntryPct {
				result[seg.Name] = pb.BrakeEntry{Pct: avg, LapsUsed: lapCount}
				effEntry[i] = avg
			}
		}
	}

	return result
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
