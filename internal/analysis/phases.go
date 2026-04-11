package analysis

import (
	"math"

	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

const (
	// steerCommitFrac is the fraction of peak |SteeringAngle| that marks the
	// boundary between entry/exit and mid phases in a corner.
	steerCommitFrac = float32(0.80)

	// minPeakSteerDeg is the minimum peak steering angle (degrees) for a corner
	// to be split into phases. Below this, the segment gets a single "full" phase.
	minPeakSteerDeg = float32(5.0)

	// steerCorrectionThresh is the minimum steering rate magnitude (rad/tick at
	// 60 Hz) on both sides of a direction reversal for it to count as a correction.
	// ~0.25 deg/tick (≈15 deg/s) — sensitive enough to catch moderate mid-corner
	// adjustments, not just emergency saves.
	steerCorrectionThresh = float32(0.0044)

	rad2deg = float32(180.0 / math.Pi)
)

// PhaseKind identifies the part of a segment a Phase covers.
type PhaseKind string

const (
	PhaseEntry PhaseKind = "entry"
	PhaseMid   PhaseKind = "mid"
	PhaseExit  PhaseKind = "exit"
	PhaseFull  PhaseKind = "full"
)

// Phase holds computed statistics for one phase (entry/mid/exit/full) of a
// track segment. Straights produce one "full" phase; corners produce up to
// three phases split by steering commitment.
type Phase struct {
	SegIndex int
	SegName  string
	Kind     PhaseKind

	SpeedEntryKPH float32 // speed of first sample in phase (km/h)
	SpeedExitKPH  float32 // speed of last sample in phase (km/h)

	BrakePct     float32 // % of samples with brake > 2%
	PeakBrakePct float32 // max brake pressure (0–100%)
	ThrottlePct float32 // % of samples at full throttle (> 95%)
	LatGAvg     float32 // average abs lateral G

	PeakSteerDeg float32 // max |SteeringAngle| in the phase (degrees)
	Corrections  int     // steering direction reversals above threshold

	ABSCount         int // samples with ABS active
	LockupSamples    int // wheel speed < 95% vehicle speed under braking
	WheelspinSamples int // wheel speed > 105% vehicle speed under power
	CoastSamples     int // throttle < 5% AND brake < 5%
	SampleCount      int
}

// ComputePhases splits each segment of a lap into steering-based phases and
// computes per-phase statistics. Straights get one "full" phase. Corners and
// chicanes are split into entry/mid/exit using the steering angle trace:
//
//   - Find peak |SteeringAngle| in the segment
//   - Entry: start → first sample reaching 80% of peak
//   - Mid: samples at ≥ 80% of peak (committed to the arc)
//   - Exit: last sample dropping below 80% of peak → end
//
// brakeEntries provides the stored brake onset positions (keyed by segment name)
// used to set effective segment entry points. Pass nil or an empty map to use
// geometric entry points only.
//
// Corners with peak steering < 5° are treated as straights (single full phase).
// Phases with 0 samples are omitted from the result.
func ComputePhases(lap *Lap, segs []trackmap.Segment, brakeEntries pb.BrakeEntryMap) []Phase {
	if len(segs) == 0 {
		return nil
	}
	if brakeEntries == nil {
		brakeEntries = pb.BrakeEntryMap{}
	}

	// Pre-compute effective entry and exit for each segment.
	effEntry := make([]float32, len(segs))
	effExit := make([]float32, len(segs))
	for i, seg := range segs {
		effEntry[i] = effectiveSegEntry(seg, brakeEntries)
		effExit[i] = seg.ExitPct
	}
	for i := 0; i < len(segs)-1; i++ {
		if effEntry[i+1] < effExit[i] {
			effExit[i] = effEntry[i+1]
		}
	}

	// Bucket samples into segments.
	segSamples := make([][]SampleData, len(segs))
	for _, s := range lap.Samples {
		idx := segmentForEffPct(s.LapDistPct, effEntry, effExit)
		if idx >= 0 {
			segSamples[idx] = append(segSamples[idx], s)
		}
	}

	var phases []Phase
	for i, seg := range segs {
		samples := segSamples[i]
		if len(samples) == 0 {
			continue
		}

		if seg.Kind == trackmap.KindStraight {
			phases = append(phases, computePhaseStats(i, seg.Name, PhaseFull, samples))
			continue
		}

		// Find peak |SteeringAngle| in this segment.
		peakSteer := float32(0)
		for _, s := range samples {
			a := abs32(s.SteeringAngle)
			if a > peakSteer {
				peakSteer = a
			}
		}

		peakDeg := peakSteer * rad2deg
		if peakDeg < minPeakSteerDeg {
			// Minimal steering — treat as a single phase.
			phases = append(phases, computePhaseStats(i, seg.Name, PhaseFull, samples))
			continue
		}

		threshold := peakSteer * steerCommitFrac

		// Find first and last sample indices at or above the commitment threshold.
		firstMid, lastMid := -1, -1
		for j, s := range samples {
			if abs32(s.SteeringAngle) >= threshold {
				if firstMid < 0 {
					firstMid = j
				}
				lastMid = j
			}
		}

		if firstMid < 0 {
			// Steering never reaches 80% — emit entry + exit only.
			mid := len(samples) / 2
			if mid > 0 {
				phases = append(phases, computePhaseStats(i, seg.Name, PhaseEntry, samples[:mid]))
			}
			if mid < len(samples) {
				phases = append(phases, computePhaseStats(i, seg.Name, PhaseExit, samples[mid:]))
			}
			continue
		}

		// Split into entry / mid / exit.
		if firstMid > 0 {
			phases = append(phases, computePhaseStats(i, seg.Name, PhaseEntry, samples[:firstMid]))
		}
		phases = append(phases, computePhaseStats(i, seg.Name, PhaseMid, samples[firstMid:lastMid+1]))
		if lastMid+1 < len(samples) {
			phases = append(phases, computePhaseStats(i, seg.Name, PhaseExit, samples[lastMid+1:]))
		}
	}

	return phases
}

// computePhaseStats accumulates statistics for a slice of samples within one phase.
func computePhaseStats(segIdx int, segName string, kind PhaseKind, samples []SampleData) Phase {
	p := Phase{
		SegIndex:    segIdx,
		SegName:     segName,
		Kind:        kind,
		SampleCount: len(samples),
	}
	if len(samples) == 0 {
		return p
	}

	p.SpeedEntryKPH = samples[0].Speed * ms2kmh
	p.SpeedExitKPH = samples[len(samples)-1].Speed * ms2kmh

	var latGSum float32
	var brakeOnCount, thrFullCount int

	for _, s := range samples {
		// Brake.
		if s.Brake > brakeOnThreshold {
			brakeOnCount++
		}
		if brkPct := s.Brake * 100; brkPct > p.PeakBrakePct {
			p.PeakBrakePct = brkPct
		}

		// Throttle.
		if s.Throttle > fullThrottleThresh {
			thrFullCount++
		}

		// Lateral G.
		latGSum += abs32(s.LatAccel) / grav

		// ABS.
		if s.ABSActive {
			p.ABSCount++
		}

		// Coast.
		if s.Throttle < 0.05 && s.Brake < 0.05 {
			p.CoastSamples++
		}

		// Lockup.
		if s.Brake > brakeOnThreshold && s.Speed > 5 {
			minWheel := min32(s.LFspeed, s.RFspeed, s.LRspeed, s.RRspeed)
			if minWheel < s.Speed*lockupRatio {
				p.LockupSamples++
			}
		}

		// Wheelspin.
		if s.Throttle > 0.5 && s.Speed > 5 {
			maxWheel := max32(s.LFspeed, s.RFspeed, s.LRspeed, s.RRspeed)
			if maxWheel > s.Speed*wheelspinRatio {
				p.WheelspinSamples++
			}
		}

		// Peak steering.
		steerDeg := abs32(s.SteeringAngle) * rad2deg
		if steerDeg > p.PeakSteerDeg {
			p.PeakSteerDeg = steerDeg
		}
	}

	n := float32(len(samples))
	p.BrakePct = 100 * float32(brakeOnCount) / n
	p.ThrottlePct = 100 * float32(thrFullCount) / n
	p.LatGAvg = latGSum / n

	// Steering corrections.
	p.Corrections = countSteeringCorrections(samples)

	return p
}

// countSteeringCorrections counts rapid direction reversals in the steering
// rate within a sample slice. A correction is a sign change in the steering
// rate where the magnitude on both sides exceeds steerCorrectionThresh.
func countSteeringCorrections(samples []SampleData) int {
	if len(samples) < 3 {
		return 0
	}

	corrections := 0
	prevRate := float32(0)

	for i := 1; i < len(samples); i++ {
		rate := samples[i].SteeringAngle - samples[i-1].SteeringAngle

		if prevRate != 0 && rate != 0 {
			// Sign change with magnitude on both sides above threshold.
			if (prevRate > 0) != (rate > 0) &&
				abs32(prevRate) > steerCorrectionThresh &&
				abs32(rate) > steerCorrectionThresh {
				corrections++
			}
		}

		if rate != 0 {
			prevRate = rate
		}
	}

	return corrections
}
