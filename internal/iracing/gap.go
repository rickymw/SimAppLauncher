package iracing

import "math"

// CarPos is one car's position on track at a point in time, as used by
// ComputeGaps. Fractional LapDistPct (0.0–1.0) combined with LapCompleted
// gives a monotonically increasing cumulative position so cars a full lap
// apart are not mistaken for being adjacent on-track.
type CarPos struct {
	CarIdx       int32
	LapDistPct   float32
	LapCompleted int32
	EstTime      float32 // seconds from S/F to this car's current position on a representative lap
}

// GapTo describes the gap from the player's car to another car.
// TimeSeconds is positive if the other car is ahead on track, negative if behind.
// LapsDelta is 0 when both cars are on the same lap.
//
// A CarIdx of -1 means "no gap" — i.e. ComputeGaps had no valid candidate in
// that direction. Callers should treat negative CarIdx as the empty sentinel
// rather than relying on the zero-value struct, because CarIdx 0 is a real
// slot in iRacing (typically the pace car).
type GapTo struct {
	CarIdx      int32
	DistPct     float32 // fractional lap distance difference, signed (+ ahead, − behind)
	TimeSeconds float32
	LapsDelta   int32 // other − me, in completed laps; 0 when on the same lap
}

// NoGap is the explicit sentinel value returned by ComputeGaps when no valid
// car exists in a given direction.
var NoGap = GapTo{CarIdx: -1}

// ComputeGaps returns the car directly ahead and directly behind the player on
// track (shortest on-track distance, not race position) along with the time
// gaps in seconds.
//
//   - me is the player's CarPos (use CarIdx < 0 to disable; the function
//     returns NoGap for both directions).
//   - others contains every other valid car (skip entries with LapDistPct < 0).
//
// The "directly ahead" car is the one with the smallest positive forward gap;
// "directly behind" is the one with the smallest absolute backward gap.
// Time gaps are derived from EstTime diffs when both cars are on the same lap,
// and fall back to `distPct * lapEstimate` when EstTimes straddle the S/F line
// or either value is missing. lapEstimate should be a rough whole-lap time in
// seconds (use me.EstTime at lap end, or a cached last-lap time).
//
// Returns (ahead, behind); either may be NoGap when no valid other car exists
// in that direction. Test for that with CarIdx < 0, never by comparing against
// the zero-value struct — CarIdx 0 is a real slot in iRacing (the pace car).
func ComputeGaps(me CarPos, others []CarPos, lapEstimate float32) (ahead, behind GapTo) {
	ahead, behind = NoGap, NoGap
	if me.CarIdx < 0 || me.LapDistPct < 0 {
		return ahead, behind
	}

	bestAhead := float32(math.MaxFloat32)
	bestBehind := float32(math.MaxFloat32)

	for _, o := range others {
		if o.CarIdx == me.CarIdx || o.LapDistPct < 0 {
			continue
		}
		fwd := shortestForward(me.LapDistPct, o.LapDistPct) // 0.0–1.0 forward to reach o
		back := 1 - fwd                                     // 0.0–1.0 backward to reach o
		if fwd < bestAhead {
			bestAhead = fwd
			ahead = GapTo{
				CarIdx:      o.CarIdx,
				DistPct:     fwd,
				TimeSeconds: estTimeGap(me, o, fwd, lapEstimate, true),
				LapsDelta:   o.LapCompleted - me.LapCompleted,
			}
		}
		if back < bestBehind {
			bestBehind = back
			behind = GapTo{
				CarIdx:      o.CarIdx,
				DistPct:     -back,
				TimeSeconds: -estTimeGap(me, o, back, lapEstimate, false),
				LapsDelta:   o.LapCompleted - me.LapCompleted,
			}
		}
	}

	return ahead, behind
}

// shortestForward returns the fractional lap distance you'd travel moving
// forward from a to b (0.0–1.0). Handles wrap-around at the S/F line.
func shortestForward(a, b float32) float32 {
	d := b - a
	if d < 0 {
		d += 1
	}
	return d
}

// estTimeGap converts a distance-fraction gap into seconds.
//
// When both cars' EstTime values are populated and they're on the same lap
// (not straddling the S/F line), |EstTime[o] − EstTime[me]| is the authoritative
// answer because it already accounts for speed variation around the lap.
//
// When EstTime isn't usable (either missing, or the two cars straddle S/F),
// we fall back to distPct × lapEstimate. lapEstimate is a coarse whole-lap
// time; any reasonable recent value is close enough for relative awareness.
//
// forward=true means o is ahead of me (so EstTime[o] should be greater).
func estTimeGap(me, o CarPos, distPct, lapEstimate float32, forward bool) float32 {
	const usableEst = 0.1 // seconds; treat anything smaller as "not populated"
	if me.EstTime > usableEst && o.EstTime > usableEst {
		// Same lap if forward-walking my pct reaches o's pct without wrapping
		// past S/F (forward=true) or backward-walking does (forward=false).
		sameLap := forward && o.LapDistPct >= me.LapDistPct ||
			!forward && o.LapDistPct <= me.LapDistPct
		if sameLap {
			diff := o.EstTime - me.EstTime
			if !forward {
				diff = -diff
			}
			if diff >= 0 {
				return diff
			}
		}
	}
	return distPct * lapEstimate
}
