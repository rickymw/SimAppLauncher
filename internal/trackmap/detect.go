package trackmap

import "math"

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

// Post-detection validation constants (latlon path only).
const (
	// S/F wraparound: corners shorter than this at the track boundary are GPS artifacts.
	wraparoundMaxM = 50.0

	// Steering/lat-G confirmation: a corner must exceed at least one of these
	// to survive validation. Corners with neither meaningful steering nor lateral
	// load are reclassified as straights.
	confirmSteerThreshRad = 0.175 // ~10 degrees
	confirmLatAccelThresh = 2.0   // m/s²

	// Speed-profile validation: corners with less speed variation than this are
	// reclassified as straights (no real deceleration occurred).
	speedDropThreshMPS = 2.8 // ~10 km/h

	// Oversized corner splitting: only corners longer than splitMinCornerM are
	// candidates. A split occurs when speed rises by at least splitReaccelMPS
	// between two distinct speed troughs.
	splitMinCornerM   = 200.0
	splitReaccelMPS   = 5.6 // ~20 km/h
	splitSpeedSmoothM = 30.0

	// Boundary refinement: thresholds for detecting cornering activity.
	// A bucket is "cornering" if steering exceeds this OR lat-G exceeds this.
	refineSteerThreshRad = 0.12  // ~7 degrees
	refineLatAccThresh   = 2.5   // m/s²
	// Minimum straight length after refinement — straights shorter than this
	// get absorbed into adjacent corners.
	refineMinStraightM = 30.0
)

const earthRadiusM = 6_371_000.0

// Sample is the minimal telemetry data required for corner detection.
// Lat and Lon are only used by DetectFromMultipleLatLon; they are zero-valued
// (and ignored) when using the default lataccel method.
// Speed and SteerAngle are used by post-detection validation in the latlon path.
type Sample struct {
	LapDistPct float32
	LatAccel   float32 // m/s²; positive = left
	Lat        float64 // decimal degrees; 0 = not available
	Lon        float64 // decimal degrees; 0 = not available
	Speed      float32 // m/s; used by speed-based validation (0 = not available)
	SteerAngle float32 // radians; used by steering confirmation filter (0 = not available)
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

// detectRawFromProfiles runs the shared detection pipeline on pre-built averaged
// abs and sign profiles, returning intermediate rawSegs before labelling.
// counts indicates which buckets had data; zero-count buckets are forward-filled.
// enterThresh and exitThresh are the hysteresis thresholds in the same units as
// absAvg (m/s² for lataccel, 1/m for latlon).
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

// detectFromProfiles runs the full pipeline and returns labelled Segments.
// Used by the lataccel path and MatchScore which don't need post-processing.
func detectFromProfiles(absAvg, signAvg []float64, counts []int, trackLengthM, enterThresh, exitThresh float64) []Segment {
	rawSegs := detectRawFromProfiles(absAvg, signAvg, counts, trackLengthM, enterThresh, exitThresh)
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

// DetectFromMultipleLatLon detects track segments using geometric curvature
// derived from GPS (lat/lon) positions rather than lateral acceleration.
// This correctly identifies pure-braking zones that produce little lateral load.
//
// Curvature is computed on bin-averaged positions (not per-sample triplets),
// which eliminates GPS quantisation noise that would otherwise dominate
// consecutive 60 Hz samples separated by less than 1 m of arc.
//
// If trackName matches a known track in the reference table, the detection
// iteratively adjusts curvature thresholds to produce the expected number of
// corner segments. Otherwise it uses default thresholds.
//
// Returns nil if no lat/lon data is available in the samples (Lat==0 and
// Lon==0 for every sample), allowing the caller to fall back to DetectFromMultiple.
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

	// Step 2: accumulate bin-averaged XY positions, speed, steering, and
	// latAccel profiles across all laps.
	xTotal := make([]float64, numBuckets)
	yTotal := make([]float64, numBuckets)
	anyCounts := make([]int, numBuckets) // total samples per bin across all laps

	speedTotal := make([]float64, numBuckets)
	speedCounts := make([]int, numBuckets)
	steerTotal := make([]float64, numBuckets)
	steerCounts := make([]int, numBuckets)
	latAccTotal := make([]float64, numBuckets)
	latAccCounts := make([]int, numBuckets)

	for _, samples := range allSamples {
		xs, ys, cnts := buildPositionProfile(samples, lat0, lon0)
		spd, spdC := buildSpeedProfile(samples)
		str, strC := buildSteerProfile(samples)
		la, _, laC := buildProfile(samples) // abs(LatAccel) profile
		for i := 0; i < numBuckets; i++ {
			xTotal[i] += xs[i]
			yTotal[i] += ys[i]
			anyCounts[i] += cnts[i]
			speedTotal[i] += spd[i]
			if spdC[i] > 0 {
				speedCounts[i]++
			}
			steerTotal[i] += str[i]
			if strC[i] > 0 {
				steerCounts[i]++
			}
			latAccTotal[i] += la[i]
			if laC[i] > 0 {
				latAccCounts[i]++
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

	// Step 5: average the validation profiles across laps.
	speedAvg := make([]float64, numBuckets)
	steerAbsAvg := make([]float64, numBuckets)
	latAccAbsAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if speedCounts[i] > 0 {
			speedAvg[i] = speedTotal[i] / float64(speedCounts[i])
		}
		if steerCounts[i] > 0 {
			steerAbsAvg[i] = steerTotal[i] / float64(steerCounts[i])
		}
		if latAccCounts[i] > 0 {
			latAccAbsAvg[i] = latAccTotal[i] / float64(latAccCounts[i])
		}
	}
	fillGaps(speedAvg, speedCounts)
	fillGaps(steerAbsAvg, steerCounts)
	fillGaps(latAccAbsAvg, latAccCounts)

	// postProcess runs the full validation pipeline on raw segments.
	postProcess := func(segs []rawSeg) []rawSeg {
		segs = trimWraparoundCorner(segs, trackLengthM)
		segs = confirmCorners(segs, steerAbsAvg, latAccAbsAvg, trackLengthM)
		segs = splitLargeCorners(segs, speedAvg, signAvg, trackLengthM)
		segs = validateCornerSpeed(segs, speedAvg, trackLengthM)
		segs = refineBoundaries(segs, steerAbsAvg, latAccAbsAvg, signAvg, trackLengthM)
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
	// Shouldn't happen, but fall back to defaults.
	rawSegs := detectRawFromProfiles(absAvg, signAvg, counts, trackLengthM, curvEnterThresh, curvExitThresh)
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
