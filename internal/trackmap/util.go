package trackmap

import (
	"fmt"
	"math"
)

// allZero returns true if every element of vals is zero.
func allZero(vals []float64) bool {
	for _, v := range vals {
		if v != 0 {
			return false
		}
	}
	return true
}

// bucketMean computes the arithmetic mean of vals[start..end] (inclusive).
func bucketMean(vals []float64, start, end int) float64 {
	if end < start {
		return 0
	}
	sum := 0.0
	for i := start; i <= end; i++ {
		sum += vals[i]
	}
	return sum / float64(end-start+1)
}

// bucketMinMax returns the min and max of vals[start..end] (inclusive).
func bucketMinMax(vals []float64, start, end int) (min, max float64) {
	min = vals[start]
	max = vals[start]
	for i := start + 1; i <= end; i++ {
		if vals[i] < min {
			min = vals[i]
		}
		if vals[i] > max {
			max = vals[i]
		}
	}
	return
}

// fillGaps fills zero-count buckets from neighbouring non-empty values.
// A forward pass propagates the last known value rightward; a backward pass
// then fills any leading zeros (buckets before the first non-empty one) from
// the earliest known value.
func fillGaps(vals []float64, counts []int) {
	// Forward pass: propagate rightward.
	last := 0.0
	for i := 0; i < len(vals); i++ {
		if counts[i] > 0 {
			last = vals[i]
		} else {
			vals[i] = last
		}
	}
	// Backward pass: fill any leading zeros that the forward pass left as 0.
	last = 0.0
	for i := len(vals) - 1; i >= 0; i-- {
		if counts[i] > 0 {
			last = vals[i]
		} else if vals[i] == 0.0 {
			vals[i] = last
		}
	}
}

// boxSmooth applies a circular box (moving average) filter with the given window.
// The window is centred on each bucket, wrapping around at boundaries.
// The effective width is 2*(window/2)+1 elements (odd, to keep the centre bucket).
func boxSmooth(vals []float64, window int) []float64 {
	n := len(vals)
	out := make([]float64, n)
	half := window / 2
	width := float64(2*half + 1) // actual number of elements summed
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := -half; j <= half; j++ {
			idx := (i + j + n) % n
			sum += vals[idx]
		}
		out[i] = sum / width
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
// the two corners have opposite latSign (product < 0) and the gap straight is
// short enough. The gap threshold scales with track length (target ~100 m).
// Merges all three into a single chicane rawSeg.
func mergeChicanes(segs []rawSeg, trackLengthM float64) []rawSeg {
	// Scale chicane gap with track length: target 100 m.
	const targetGapM = 100.0
	// Max total chicane length: corners that are individually large and far apart
	// (e.g. Redgate + Hollywood at Donington) are separate corners, not a chicane.
	const maxChicaneM = 400.0
	maxGap := int(0.018 * numBuckets) // fallback: 1.8% ≈ 18 buckets
	maxTotal := int(0.15 * numBuckets) // fallback: 15%
	if trackLengthM > 0 {
		maxGap = max(1, int(targetGapM/trackLengthM*float64(numBuckets)))
		maxTotal = max(1, int(maxChicaneM/trackLengthM*float64(numBuckets)))
	}

	i := 0
	for i+2 < len(segs) {
		a, mid, b := segs[i], segs[i+1], segs[i+2]
		totalLen := a.length() + mid.length() + b.length()
		if !a.isCorner || mid.isCorner || !b.isCorner ||
			mid.length() > maxGap || a.latSign*b.latSign >= 0 ||
			totalLen > maxTotal {
			i++
			continue
		}
		// Merge all three into one chicane segment.
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
		// Do not increment i — re-check this position in case of back-to-back chicanes.
	}
	return segs
}

// MatchScore computes how well the given lap samples match the stored segment
// boundaries. For each segment boundary (entry pct of each segment after the
// first), it checks whether the current lap also shows a corner/straight
// transition within a tolerance of ±0.02 (2% of lap distance). Returns a
// value 0.0–1.0 where 1.0 = all boundaries matched.
//
// trackLengthM is used to scale the smoothing window (same as Detect).
// If len(segs) <= 1, returns 1.0 (no interior boundaries to check).
func MatchScore(samples []Sample, segs []Segment, trackLengthM float64) float32 {
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

	// Use the same window as Detect: ~15m scaled to track length.
	window := max(1, int(15.0/trackLengthM*numBuckets))
	smoothed := boxSmooth(absAvg, window)
	isCorner := hysteresis(smoothed, latAccelEnterThresh, latAccelExitThresh)

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
