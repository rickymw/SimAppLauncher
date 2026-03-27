package trackmap

import (
	"fmt"
	"math"
)

const numBuckets = 1000 // 0.1% resolution

// Sample is the minimal telemetry data required for corner detection.
type Sample struct {
	LapDistPct float32
	LatAccel   float32 // m/s²; positive = left
}

// rawSeg is an intermediate segment used during detection before final labelling.
type rawSeg struct {
	isCorner  bool
	isChicane bool
	start     int     // inclusive bucket index
	end       int     // inclusive bucket index
	latSign   float64 // length-weighted average sign of LatAccel
}

func (r rawSeg) length() int { return r.end - r.start + 1 }

// Detect analyses a slice of samples and returns a labelled []Segment.
// trackLengthM is used to scale the smoothing window and metre values.
// Returns nil if samples is empty.
func Detect(samples []Sample, trackLengthM float64) []Segment {
	if len(samples) == 0 {
		return nil
	}

	// Step 1: bucket abs(LatAccel) and LatAccel sign into 1000 buckets.
	absSum := make([]float64, numBuckets)
	signSum := make([]float64, numBuckets)
	counts := make([]int, numBuckets)

	for _, s := range samples {
		b := int(s.LapDistPct * numBuckets)
		if b < 0 {
			b = 0
		}
		if b >= numBuckets {
			b = numBuckets - 1
		}
		absSum[b] += math.Abs(float64(s.LatAccel))
		if s.LatAccel > 0 {
			signSum[b] += 1.0
		} else if s.LatAccel < 0 {
			signSum[b] -= 1.0
		}
		counts[b]++
	}

	// Average within each bucket.
	absAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if counts[i] > 0 {
			absAvg[i] = absSum[i] / float64(counts[i])
			signAvg[i] = signSum[i] / float64(counts[i])
		}
	}

	// Step 2: forward-fill empty buckets.
	fillGaps(absAvg, counts)
	fillGaps(signAvg, counts)

	// Step 3: box-smooth the abs signal with a window proportional to ~15m.
	window := max(1, int(15.0/trackLengthM*numBuckets))
	smoothed := boxSmooth(absAvg, window)

	// Step 4: hysteresis classification (enter ≥5, exit <2.5).
	isCorner := hysteresis(smoothed, 5.0, 2.5)

	// Step 5: group consecutive buckets into rawSegs.
	rawSegs := groupBuckets(isCorner, signAvg)

	// Step 6: merge short segments repeatedly until stable.
	minStraightBuckets := max(1, int(0.012*numBuckets)) // 12
	minCornerBuckets := max(1, int(0.006*numBuckets))   // 6
	rawSegs = mergeShort(rawSegs, minStraightBuckets, minCornerBuckets)

	// Step 7: merge chicanes: [corner, short-straight, corner] with opposite signs.
	rawSegs = mergeChicanes(rawSegs)

	// Step 8: label and convert to []Segment.
	return labelSegments(rawSegs, trackLengthM)
}

// fillGaps forward-fills zero-count buckets from the last non-empty value.
func fillGaps(vals []float64, counts []int) {
	last := 0.0
	for i := 0; i < len(vals); i++ {
		if counts[i] > 0 {
			last = vals[i]
		} else {
			vals[i] = last
		}
	}
}

// boxSmooth applies a circular box (moving average) filter with the given window.
// The window is centred on each bucket, wrapping around at boundaries.
func boxSmooth(vals []float64, window int) []float64 {
	n := len(vals)
	out := make([]float64, n)
	half := window / 2
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := -half; j <= half; j++ {
			idx := (i + j + n) % n
			sum += vals[idx]
		}
		out[i] = sum / float64(window+1)
	}
	return out
}

// hysteresis classifies each bucket as corner (true) or straight (false).
// State flips to true when value >= enter, back to false when value < exit.
func hysteresis(vals []float64, enter, exit float64) []bool {
	result := make([]bool, len(vals))
	inCorner := false
	for i, v := range vals {
		if !inCorner && v >= enter {
			inCorner = true
		} else if inCorner && v < exit {
			inCorner = false
		}
		result[i] = inCorner
	}
	return result
}

// groupBuckets converts a bool slice into rawSeg structs.
func groupBuckets(isCorner []bool, signAvg []float64) []rawSeg {
	if len(isCorner) == 0 {
		return nil
	}
	var segs []rawSeg
	cur := rawSeg{isCorner: isCorner[0], start: 0}
	for i := 1; i < len(isCorner); i++ {
		if isCorner[i] != cur.isCorner {
			cur.end = i - 1
			cur.latSign = avgSign(signAvg, cur.start, cur.end)
			segs = append(segs, cur)
			cur = rawSeg{isCorner: isCorner[i], start: i}
		}
	}
	cur.end = len(isCorner) - 1
	cur.latSign = avgSign(signAvg, cur.start, cur.end)
	segs = append(segs, cur)
	return segs
}

// avgSign computes the average of signAvg[start..end] (inclusive).
func avgSign(signAvg []float64, start, end int) float64 {
	sum := 0.0
	for i := start; i <= end; i++ {
		sum += signAvg[i]
	}
	n := end - start + 1
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// mergeShort iteratively merges segments that are too short until stable.
func mergeShort(segs []rawSeg, minStraight, minCorner int) []rawSeg {
	for {
		merged := false
		for i := 0; i < len(segs); i++ {
			s := segs[i]
			minLen := minStraight
			if s.isCorner {
				minLen = minCorner
			}
			if s.length() < minLen {
				segs = mergeAt(segs, mergeIdx(segs, i))
				merged = true
				break // restart scan
			}
		}
		if !merged {
			break
		}
	}
	return segs
}

// mergeIdx returns the index at which to call mergeAt for segment i.
// If i is first → merge with next (return 0).
// If i is last  → merge with prev (return len-2).
// Otherwise     → merge with the smaller neighbor.
func mergeIdx(segs []rawSeg, i int) int {
	if i == 0 {
		return 0
	}
	if i == len(segs)-1 {
		return len(segs) - 2
	}
	// Merge with the smaller neighbor.
	if segs[i-1].length() <= segs[i+1].length() {
		return i - 1
	}
	return i
}

// mergeAt merges segs[i] and segs[i+1] into a single rawSeg.
// The merged segment inherits isCorner from whichever half is longer;
// latSign is the length-weighted average; start/end span both.
func mergeAt(segs []rawSeg, i int) []rawSeg {
	if i < 0 || i+1 >= len(segs) {
		return segs
	}
	a, b := segs[i], segs[i+1]
	merged := rawSeg{
		start: a.start,
		end:   b.end,
	}
	// isCorner from the longer half.
	if a.length() >= b.length() {
		merged.isCorner = a.isCorner
	} else {
		merged.isCorner = b.isCorner
	}
	// length-weighted latSign.
	total := float64(a.length() + b.length())
	merged.latSign = (a.latSign*float64(a.length()) + b.latSign*float64(b.length())) / total

	result := make([]rawSeg, 0, len(segs)-1)
	result = append(result, segs[:i]...)
	result = append(result, merged)
	result = append(result, segs[i+2:]...)
	return result
}

// mergeChicanes scans for [corner, short-straight, corner] triplets where
// the two corners have opposite latSign (product < 0) and the straight is
// <= 18 buckets. Merges all three into a single chicane rawSeg.
func mergeChicanes(segs []rawSeg) []rawSeg {
	maxChicaneStraight := int(0.018 * numBuckets) // 18
	for i := 0; i+2 < len(segs); i++ {
		a, mid, b := segs[i], segs[i+1], segs[i+2]
		if !a.isCorner || mid.isCorner || !b.isCorner {
			continue
		}
		if mid.length() > maxChicaneStraight {
			continue
		}
		if a.latSign*b.latSign >= 0 {
			continue // same direction — not a chicane
		}
		// Merge all three.
		total := float64(a.length() + mid.length() + b.length())
		merged := rawSeg{
			isCorner:  true,
			isChicane: true,
			start:     a.start,
			end:       b.end,
			latSign:   (a.latSign*float64(a.length()) + mid.latSign*float64(mid.length()) + b.latSign*float64(b.length())) / total,
		}
		result := make([]rawSeg, 0, len(segs)-2)
		result = append(result, segs[:i]...)
		result = append(result, merged)
		result = append(result, segs[i+3:]...)
		segs = result
		i-- // re-check from this position
	}
	return segs
}

// MatchScore computes how well the given lap samples match the stored segment
// boundaries. For each segment boundary (entry pct of each segment after the
// first), it checks whether the current lap also shows a corner/straight
// transition within a tolerance of ±0.02 (2% of lap distance). Returns a
// value 0.0–1.0 where 1.0 = all boundaries matched.
//
// If len(segs) <= 1, returns 1.0 (no interior boundaries to check).
func MatchScore(samples []Sample, segs []Segment) float32 {
	if len(segs) <= 1 {
		return 1.0
	}

	// Re-run the same classification pipeline as Detect on the current lap.
	absSum := make([]float64, numBuckets)
	signSum := make([]float64, numBuckets)
	counts := make([]int, numBuckets)

	for _, s := range samples {
		b := int(s.LapDistPct * numBuckets)
		if b < 0 {
			b = 0
		}
		if b >= numBuckets {
			b = numBuckets - 1
		}
		absSum[b] += math.Abs(float64(s.LatAccel))
		if s.LatAccel > 0 {
			signSum[b] += 1.0
		} else if s.LatAccel < 0 {
			signSum[b] -= 1.0
		}
		counts[b]++
	}

	absAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if counts[i] > 0 {
			absAvg[i] = absSum[i] / float64(counts[i])
			signAvg[i] = signSum[i] / float64(counts[i])
		}
	}

	fillGaps(absAvg, counts)
	fillGaps(signAvg, counts)

	// Use a fixed 15m window; if we have no track length info use a small default.
	// Since MatchScore doesn't receive trackLengthM, use 1 as the minimum window.
	smoothed := boxSmooth(absAvg, 1)
	isCorner := hysteresis(smoothed, 5.0, 2.5)

	// Build a bool slice of transitions: transition[i] = true if isCorner[i] != isCorner[i-1].
	hasTransition := make([]bool, numBuckets)
	for i := 1; i < numBuckets; i++ {
		if isCorner[i] != isCorner[i-1] {
			hasTransition[i] = true
		}
	}

	// Check each interior boundary (segments[1], segments[2], ...).
	const tolerance = 20 // ±2% = ±20 buckets
	matched := 0
	total := len(segs) - 1
	for _, seg := range segs[1:] {
		b := int(seg.EntryPct * float32(numBuckets))
		lo := b - tolerance
		if lo < 0 {
			lo = 0
		}
		hi := b + tolerance
		if hi >= numBuckets {
			hi = numBuckets - 1
		}
		for j := lo; j <= hi; j++ {
			if hasTransition[j] {
				matched++
				break
			}
		}
	}

	return float32(matched) / float32(total)
}

// labelSegments converts rawSegs to named Segments with pct and metre values.
func labelSegments(rawSegs []rawSeg, trackLengthM float64) []Segment {
	segments := make([]Segment, 0, len(rawSegs))
	tNum := 0 // corner counter
	sNum := 0 // straight counter

	for _, r := range rawSegs {
		entryPct := float32(r.start) / float32(numBuckets)
		exitPct := float32(r.end+1) / float32(numBuckets)
		if exitPct > 1.0 {
			exitPct = 1.0
		}

		seg := Segment{
			EntryPct: entryPct,
			ExitPct:  exitPct,
			EntryM:   entryPct * float32(trackLengthM),
			ExitM:    exitPct * float32(trackLengthM),
		}

		if !r.isCorner {
			sNum++
			seg.Name = fmt.Sprintf("S%d", sNum)
			seg.Kind = KindStraight
		} else if r.isChicane {
			tNum++
			seg.Name = fmt.Sprintf("T%d-%d", tNum, tNum+1)
			seg.Kind = KindChicane
			tNum++ // chicane consumes two turn numbers
		} else {
			tNum++
			seg.Name = fmt.Sprintf("T%d", tNum)
			seg.Kind = KindCorner
		}

		segments = append(segments, seg)
	}
	return segments
}
