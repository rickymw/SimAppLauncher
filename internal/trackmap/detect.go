package trackmap

import (
	"fmt"
	"math"
)

const numBuckets = 1000 // 0.1% resolution

// Detection thresholds for the lataccel method (m/s²).
const (
	latAccelEnterThresh = 5.0
	latAccelExitThresh  = 2.5
)

// Detection thresholds for the latlon curvature method (1/m).
// curvEnterThresh = 0.004 → R ≤ 250 m (enter corner).
// curvExitThresh  = 0.0015 → R > 667 m (exit to straight).
const (
	curvEnterThresh = 0.004
	curvExitThresh  = 0.0015
)

const earthRadiusM = 6_371_000.0

// Sample is the minimal telemetry data required for corner detection.
// Lat and Lon are only used by DetectFromMultipleLatLon; they are zero-valued
// (and ignored) when using the default lataccel method.
type Sample struct {
	LapDistPct float32
	LatAccel   float32 // m/s²; positive = left
	Lat        float64 // decimal degrees; 0 = not available
	Lon        float64 // decimal degrees; 0 = not available
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

// buildProfile buckets abs(LatAccel) and LatAccel sign for a single lap's
// samples into numBuckets bins. Returns per-bucket average abs values,
// per-bucket average sign values, and per-bucket sample counts.
func buildProfile(samples []Sample) (latAbs []float64, latSign []float64, counts []int) {
	absSum := make([]float64, numBuckets)
	signSum := make([]float64, numBuckets)
	counts = make([]int, numBuckets)

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

	latAbs = make([]float64, numBuckets)
	latSign = make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if counts[i] > 0 {
			latAbs[i] = absSum[i] / float64(counts[i])
			latSign[i] = signSum[i] / float64(counts[i])
		}
	}
	return latAbs, latSign, counts
}

// detectFromProfiles runs the shared detection pipeline on pre-built averaged
// abs and sign profiles. counts indicates which buckets had data; zero-count
// buckets are forward-filled. enterThresh and exitThresh are the hysteresis
// thresholds in the same units as absAvg (m/s² for lataccel, 1/m for latlon).
func detectFromProfiles(absAvg, signAvg []float64, counts []int, trackLengthM, enterThresh, exitThresh float64) []Segment {
	// Forward-fill empty buckets.
	fillGaps(absAvg, counts)
	fillGaps(signAvg, counts)

	// Box-smooth the abs signal with a window proportional to ~15m.
	window := max(1, int(15.0/trackLengthM*numBuckets))
	smoothed := boxSmooth(absAvg, window)

	// Hysteresis classification.
	isCorner := hysteresis(smoothed, enterThresh, exitThresh)

	// Group consecutive buckets into rawSegs.
	rawSegs := groupBuckets(isCorner, signAvg)

	// Merge short segments repeatedly until stable.
	minStraightBuckets := max(1, int(0.012*numBuckets)) // 12
	minCornerBuckets := max(1, int(0.006*numBuckets))   // 6
	rawSegs = mergeShort(rawSegs, minStraightBuckets, minCornerBuckets)

	// Merge chicanes: [corner, short-straight, corner] with opposite signs.
	rawSegs = mergeChicanes(rawSegs, trackLengthM)

	return labelSegments(rawSegs, trackLengthM)
}

// DetectFromMultiple averages the LatAccel profiles of all provided lap sample
// slices before running corner detection. This produces more stable segment
// boundaries than using a single lap. allSamples must be non-empty.
func DetectFromMultiple(allSamples [][]Sample, trackLengthM float64) []Segment {
	if len(allSamples) == 0 {
		return nil
	}

	// Accumulate per-lap profiles.
	absTotal := make([]float64, numBuckets)
	signTotal := make([]float64, numBuckets)
	// Track which buckets had data in any lap (for fillGaps).
	anyCounts := make([]int, numBuckets)

	for _, samples := range allSamples {
		lapAbs, lapSign, lapCounts := buildProfile(samples)
		for i := 0; i < numBuckets; i++ {
			absTotal[i] += lapAbs[i]
			signTotal[i] += lapSign[i]
			if lapCounts[i] > 0 {
				anyCounts[i]++
			}
		}
	}

	// Average element-wise across laps (only laps that had data in each bucket).
	absAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if anyCounts[i] > 0 {
			n := float64(anyCounts[i])
			absAvg[i] = absTotal[i] / n
			signAvg[i] = signTotal[i] / n
		}
	}

	return detectFromProfiles(absAvg, signAvg, anyCounts, trackLengthM, latAccelEnterThresh, latAccelExitThresh)
}

// Detect analyses a slice of samples and returns a labelled []Segment.
// trackLengthM is used to scale the smoothing window and metre values.
// Returns nil if samples is empty.
// Detect is a thin wrapper around DetectFromMultiple for a single lap.
func Detect(samples []Sample, trackLengthM float64) []Segment {
	if len(samples) == 0 {
		return nil
	}
	return DetectFromMultiple([][]Sample{samples}, trackLengthM)
}

// project converts (lat, lon) in decimal degrees to local (x, y) in metres
// relative to the origin (lat0, lon0), using an equirectangular projection.
func project(lat, lon, lat0, lon0 float64) (x, y float64) {
	cosLat0 := math.Cos(lat0 * math.Pi / 180.0)
	x = (lon - lon0) * math.Pi / 180.0 * cosLat0 * earthRadiusM
	y = (lat - lat0) * math.Pi / 180.0 * earthRadiusM
	return
}

// signedCurvature returns the signed curvature (1/m) of the arc through three
// consecutive XY points A→B→C. Positive = left turn. Returns 0 for degenerate
// input (coincident points or collinear within floating-point precision).
func signedCurvature(ax, ay, bx, by, cx, cy float64) float64 {
	abx, aby := bx-ax, by-ay
	bcx, bcy := cx-bx, cy-by
	acx, acy := cx-ax, cy-ay
	cross := abx*bcy - aby*bcx
	ab := math.Hypot(abx, aby)
	bc := math.Hypot(bcx, bcy)
	ac := math.Hypot(acx, acy)
	denom := ab * bc * ac
	if denom < 1e-10 {
		return 0
	}
	return 2 * cross / denom
}

// buildPositionProfile accumulates projected XY positions for a single lap's
// samples into numBuckets bins. Returns per-bucket XY sums and sample counts.
// Samples with Lat==0 and Lon==0 are skipped. lat0/lon0 is the projection origin.
func buildPositionProfile(samples []Sample, lat0, lon0 float64) (xSum, ySum []float64, counts []int) {
	xSum = make([]float64, numBuckets)
	ySum = make([]float64, numBuckets)
	counts = make([]int, numBuckets)
	for _, s := range samples {
		if s.Lat == 0 && s.Lon == 0 {
			continue
		}
		b := int(s.LapDistPct * numBuckets)
		if b < 0 {
			b = 0
		}
		if b >= numBuckets {
			b = numBuckets - 1
		}
		x, y := project(s.Lat, s.Lon, lat0, lon0)
		xSum[b] += x
		ySum[b] += y
		counts[b]++
	}
	return
}

// DetectFromMultipleLatLon detects track segments using geometric curvature
// derived from GPS (lat/lon) positions rather than lateral acceleration.
// This correctly identifies pure-braking zones that produce little lateral load.
//
// Curvature is computed on bin-averaged positions (not per-sample triplets),
// which eliminates GPS quantisation noise that would otherwise dominate
// consecutive 60 Hz samples separated by less than 1 m of arc.
//
// Returns nil if no lat/lon data is available in the samples (Lat==0 and
// Lon==0 for every sample), allowing the caller to fall back to DetectFromMultiple.
func DetectFromMultipleLatLon(allSamples [][]Sample, trackLengthM float64) []Segment {
	if len(allSamples) == 0 {
		return nil
	}

	// Step 1: shared projection origin from all samples.
	var latSum, lonSum float64
	var nPos int
	for _, samples := range allSamples {
		for _, s := range samples {
			if s.Lat != 0 || s.Lon != 0 {
				latSum += s.Lat
				lonSum += s.Lon
				nPos++
			}
		}
	}
	if nPos == 0 {
		return nil // lat/lon not available in telemetry
	}
	lat0 := latSum / float64(nPos)
	lon0 := lonSum / float64(nPos)

	// Step 2: accumulate bin-averaged XY positions across all laps.
	xTotal := make([]float64, numBuckets)
	yTotal := make([]float64, numBuckets)
	anyCounts := make([]int, numBuckets) // total samples per bin across all laps
	for _, samples := range allSamples {
		xs, ys, cnts := buildPositionProfile(samples, lat0, lon0)
		for i := 0; i < numBuckets; i++ {
			xTotal[i] += xs[i]
			yTotal[i] += ys[i]
			anyCounts[i] += cnts[i]
		}
	}

	hasData := false
	for _, c := range anyCounts {
		if c > 0 {
			hasData = true
			break
		}
	}
	if !hasData {
		return nil
	}

	// Step 3: average positions per bin then forward-fill empty bins.
	xAvg := make([]float64, numBuckets)
	yAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if anyCounts[i] > 0 {
			xAvg[i] = xTotal[i] / float64(anyCounts[i])
			yAvg[i] = yTotal[i] / float64(anyCounts[i])
		}
	}
	fillGaps(xAvg, anyCounts)
	fillGaps(yAvg, anyCounts)

	// Step 4: compute curvature once on the clean averaged positions.
	// Use bins spaced ~20 m apart for the (prev, centre, next) triplet so
	// that each arm length is large relative to GPS quantisation noise (~0.1 m).
	spacing := max(1, int(20.0/trackLengthM*numBuckets))
	absAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		prev := (i - spacing + numBuckets) % numBuckets
		next := (i + spacing) % numBuckets
		kappa := signedCurvature(xAvg[prev], yAvg[prev], xAvg[i], yAvg[i], xAvg[next], yAvg[next])
		absAvg[i] = math.Abs(kappa)
		if kappa > 0 {
			signAvg[i] = 1.0
		} else if kappa < 0 {
			signAvg[i] = -1.0
		}
	}

	return detectFromProfiles(absAvg, signAvg, anyCounts, trackLengthM, curvEnterThresh, curvExitThresh)
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
	maxGap := int(0.018 * numBuckets) // fallback: 1.8% ≈ 18 buckets
	if trackLengthM > 0 {
		maxGap = max(1, int(targetGapM/trackLengthM*float64(numBuckets)))
	}

	i := 0
	for i+2 < len(segs) {
		a, mid, b := segs[i], segs[i+1], segs[i+2]
		if !a.isCorner || mid.isCorner || !b.isCorner ||
			mid.length() > maxGap || a.latSign*b.latSign >= 0 {
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
