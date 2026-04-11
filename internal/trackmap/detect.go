package trackmap

import "math"

const numBuckets = 1000 // 0.1% resolution

// Detection thresholds for the latlon curvature method (1/m).
// curvEnterThresh = 0.004 → R ≤ 250 m (enter corner).
// curvExitThresh  = 0.0015 → R > 667 m (exit to straight).
const (
	curvEnterThresh = 0.004
	curvExitThresh  = 0.0015
)

// Post-detection geometry constants.
const (
	// S/F wraparound: corners shorter than this at the track boundary are GPS artifacts.
	wraparoundMaxM = 50.0

	// minCornerArcM: corners with a road arc shorter than this are GPS noise.
	// A 250 m radius corner (the curvature entry threshold) subtends ~30 m of arc
	// at 7° of heading change — below this the "corner" is not a meaningful feature.
	minCornerArcM = 30.0

	// Oversized corner splitting: only corners longer than splitMinCornerM are
	// candidates. A split occurs when speed rises by at least splitReaccelMPS
	// between two distinct speed troughs.
	splitMinCornerM   = 200.0
	splitReaccelMPS   = 5.6 // ~20 km/h
	splitSpeedSmoothM = 30.0
)

const earthRadiusM = 6_371_000.0

// Sample is the minimal telemetry data required for corner detection.
// Lat and Lon are used by DetectFromMultipleLatLon for GPS curvature detection.
// Speed is used by splitLargeCorners for multi-apex detection.
// LapDistPct is the common x-axis for all profiles.
type Sample struct {
	LapDistPct float32
	Lat        float64 // decimal degrees; 0 = not available
	Lon        float64 // decimal degrees; 0 = not available
	Speed      float32 // m/s; used by splitLargeCorners (0 = not available)
}

// rawSeg is an intermediate segment used during detection before final labelling.
type rawSeg struct {
	isCorner  bool
	isChicane bool
	start     int     // inclusive bucket index
	end       int     // inclusive bucket index
	latSign   float64 // length-weighted average sign of curvature
}

func (r rawSeg) length() int { return r.end - r.start + 1 }

// detectRawFromProfiles runs the shared detection pipeline on pre-built averaged
// abs and sign profiles, returning intermediate rawSegs before labelling.
// counts indicates which buckets had data; zero-count buckets are forward-filled.
// enterThresh and exitThresh are the hysteresis thresholds in curvature units (1/m).
func detectRawFromProfiles(absAvg, signAvg []float64, counts []int, trackLengthM, enterThresh, exitThresh float64) []rawSeg {
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

	return rawSegs
}

// DetectFromMultipleLatLon detects track segments using geometric curvature
// derived from GPS (lat/lon) positions rather than driver inputs.
// This is the authoritative detection method — segment boundaries are set by
// where the road curves, independent of how the driver is cornering.
//
// Curvature is computed on bin-averaged positions (not per-sample triplets),
// which eliminates GPS quantisation noise that would otherwise dominate
// consecutive 60 Hz samples separated by less than 1 m of arc.
//
// If targetCorners > 0, the detection iteratively adjusts curvature thresholds
// to produce the expected number of corner segments.
//
// Returns nil if no lat/lon data is available in the samples (Lat==0 and
// Lon==0 for every sample).
func DetectFromMultipleLatLon(allSamples [][]Sample, trackLengthM float64, targetCorners int) []Segment {
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

	// Step 2: accumulate bin-averaged XY positions and speed profiles across all laps.
	xTotal := make([]float64, numBuckets)
	yTotal := make([]float64, numBuckets)
	anyCounts := make([]int, numBuckets) // total samples per bin across all laps

	speedTotal := make([]float64, numBuckets)
	speedCounts := make([]int, numBuckets)

	for _, samples := range allSamples {
		xs, ys, cnts := buildPositionProfile(samples, lat0, lon0)
		spd, spdC := buildSpeedProfile(samples)
		for i := 0; i < numBuckets; i++ {
			xTotal[i] += xs[i]
			yTotal[i] += ys[i]
			anyCounts[i] += cnts[i]
			speedTotal[i] += spd[i]
			if spdC[i] > 0 {
				speedCounts[i]++
			}
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

	// Step 4: compute curvature on the clean averaged positions.
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

	// Step 5: average speed profile across laps.
	speedAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if speedCounts[i] > 0 {
			speedAvg[i] = speedTotal[i] / float64(speedCounts[i])
		}
	}
	fillGaps(speedAvg, speedCounts)

	// postProcess applies geometry-only validation to raw segments.
	postProcess := func(segs []rawSeg) []rawSeg {
		segs = trimWraparoundCorner(segs, trackLengthM)
		segs = filterShortArcs(segs, trackLengthM)
		segs = splitLargeCorners(segs, speedAvg, signAvg, trackLengthM)
		return segs
	}

	// Step 6: detect segments. If a target corner count is known, search
	// across curvature thresholds for the best match.
	if targetCorners <= 0 {
		// No reference — use default thresholds.
		rawSegs := detectRawFromProfiles(absAvg, signAvg, anyCounts, trackLengthM, curvEnterThresh, curvExitThresh)
		rawSegs = postProcess(rawSegs)
		return labelSegments(rawSegs, trackLengthM)
	}

	rawSegs := searchThresholds(absAvg, signAvg, anyCounts, trackLengthM, targetCorners, postProcess)
	return labelSegments(rawSegs, trackLengthM)
}

// ---------------------------------------------------------------------------
// Threshold search for target-guided detection
// ---------------------------------------------------------------------------

// searchThresholds tries a range of curvature enter thresholds and returns the
// raw segments whose corner count best matches targetCorners. The exit threshold
// is kept proportional to the enter threshold (same ratio as the defaults).
// postProcess is called on each candidate to apply validation steps.
func searchThresholds(absAvg, signAvg []float64, counts []int, trackLengthM float64, targetCorners int, postProcess func([]rawSeg) []rawSeg) []rawSeg {
	// Threshold candidates: from very sensitive to very strict.
	// The default ratio exit/enter = 0.0015/0.004 = 0.375.
	const exitRatio = 0.375
	candidates := []float64{
		0.0015, 0.002, 0.0025, 0.003, 0.0035,
		0.004, // default
		0.0045, 0.005, 0.006, 0.007, 0.008,
	}

	type result struct {
		segs  []rawSeg
		diff  int
		enter float64
	}

	var best *result
	for _, enter := range candidates {
		exit := enter * exitRatio

		// Work on copies since detectRawFromProfiles mutates the profile slices
		// (fillGaps modifies in-place). Clone them for each iteration.
		absCopy := make([]float64, len(absAvg))
		copy(absCopy, absAvg)
		signCopy := make([]float64, len(signAvg))
		copy(signCopy, signAvg)
		countsCopy := make([]int, len(counts))
		copy(countsCopy, counts)

		rawSegs := detectRawFromProfiles(absCopy, signCopy, countsCopy, trackLengthM, enter, exit)
		rawSegs = postProcess(rawSegs)
		corners := countCornerSegs(rawSegs)
		diff := corners - targetCorners
		if diff < 0 {
			diff = -diff
		}

		if best == nil || diff < best.diff || (diff == best.diff && math.Abs(enter-curvEnterThresh) < math.Abs(best.enter-curvEnterThresh)) {
			best = &result{segs: rawSegs, diff: diff, enter: enter}
		}

		// Perfect match — stop early.
		if diff == 0 {
			break
		}
	}

	if best != nil {
		return best.segs
	}
	// Shouldn't happen, but fall back to defaults. Copy slices since
	// earlier iterations may have mutated the originals via fillGaps.
	absCopy := make([]float64, len(absAvg))
	copy(absCopy, absAvg)
	signCopy := make([]float64, len(signAvg))
	copy(signCopy, signAvg)
	countsCopy := make([]int, len(counts))
	copy(countsCopy, counts)
	rawSegs := detectRawFromProfiles(absCopy, signCopy, countsCopy, trackLengthM, curvEnterThresh, curvExitThresh)
	return postProcess(rawSegs)
}

// countCornerSegs returns the number of corner segments (including chicanes).
func countCornerSegs(segs []rawSeg) int {
	n := 0
	for _, s := range segs {
		if s.isCorner {
			n++
		}
	}
	return n
}
