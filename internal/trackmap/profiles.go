package trackmap

import "math"

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

// buildSpeedProfile buckets Speed (m/s) for a single lap into numBuckets bins.
// Returns per-bucket average speed and sample counts.
func buildSpeedProfile(samples []Sample) (speedAvg []float64, counts []int) {
	sums := make([]float64, numBuckets)
	counts = make([]int, numBuckets)
	for _, s := range samples {
		if s.Speed == 0 {
			continue
		}
		b := int(s.LapDistPct * numBuckets)
		if b < 0 {
			b = 0
		}
		if b >= numBuckets {
			b = numBuckets - 1
		}
		sums[b] += float64(s.Speed)
		counts[b]++
	}
	speedAvg = make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if counts[i] > 0 {
			speedAvg[i] = sums[i] / float64(counts[i])
		}
	}
	return
}

// buildSteerProfile buckets abs(SteerAngle) (radians) for a single lap into
// numBuckets bins. Returns per-bucket average absolute steering angle and counts.
func buildSteerProfile(samples []Sample) (steerAbsAvg []float64, counts []int) {
	sums := make([]float64, numBuckets)
	counts = make([]int, numBuckets)
	for _, s := range samples {
		b := int(s.LapDistPct * numBuckets)
		if b < 0 {
			b = 0
		}
		if b >= numBuckets {
			b = numBuckets - 1
		}
		sums[b] += math.Abs(float64(s.SteerAngle))
		counts[b]++
	}
	steerAbsAvg = make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		if counts[i] > 0 {
			steerAbsAvg[i] = sums[i] / float64(counts[i])
		}
	}
	return
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
