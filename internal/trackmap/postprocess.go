package trackmap

// Post-detection validation functions for the latlon detection path.
// These functions transform []rawSeg after initial detection using only
// geometric signals (arc length, track topology) — no driver inputs.

// trimWraparoundCorner removes tiny GPS-artifact corners at the start/finish
// line. If the first or last segment is a corner shorter than wraparoundMaxM,
// it is reclassified as a straight and merged with its neighbor.
func trimWraparoundCorner(segs []rawSeg, trackLengthM float64) []rawSeg {
	if len(segs) < 2 {
		return segs
	}
	maxBuckets := max(1, int(wraparoundMaxM/trackLengthM*numBuckets))

	// Check first segment.
	if segs[0].isCorner && segs[0].length() <= maxBuckets {
		segs[0].isCorner = false
		segs[0].isChicane = false
		segs = mergeAt(segs, 0) // merge with segs[1]
	}

	// Check last segment.
	if len(segs) >= 2 {
		last := len(segs) - 1
		if segs[last].isCorner && segs[last].length() <= maxBuckets {
			segs[last].isCorner = false
			segs[last].isChicane = false
			segs = mergeAt(segs, last-1) // merge with previous
		}
	}

	return segs
}

// filterShortArcs removes corners whose road arc is shorter than minCornerArcM.
// Short curvature pulses in the GPS trace are typically quantisation noise
// rather than real corners. This replaces the old confirmCorners function which
// used driver steering/lat-G to validate corners.
func filterShortArcs(segs []rawSeg, trackLengthM float64) []rawSeg {
	changed := false
	for i := range segs {
		if !segs[i].isCorner {
			continue
		}
		arcM := float64(segs[i].length()) / numBuckets * trackLengthM
		if arcM < minCornerArcM {
			segs[i].isCorner = false
			segs[i].isChicane = false
			changed = true
		}
	}
	if !changed {
		return segs
	}
	minStraightBuckets := max(1, int(0.012*numBuckets))
	minCornerBuckets := max(1, int(0.006*numBuckets))
	return mergeShort(segs, minStraightBuckets, minCornerBuckets)
}

// splitLargeCorners examines oversized corner segments for multiple speed troughs
// separated by significant re-acceleration. Where found, the corner is split at
// the speed peak between the troughs, producing two independent corner segments.
func splitLargeCorners(segs []rawSeg, speedAvg, signAvg []float64, trackLengthM float64) []rawSeg {
	if allZero(speedAvg) {
		return segs
	}

	minBuckets := max(1, int(splitMinCornerM/trackLengthM*numBuckets))
	smoothWindow := max(1, int(splitSpeedSmoothM/trackLengthM*numBuckets))

	// Smooth speed for trough detection (non-circular, segment-local).
	smoothedSpeed := boxSmooth(speedAvg, smoothWindow)

	var result []rawSeg
	for _, seg := range segs {
		if !seg.isCorner || seg.length() < minBuckets {
			result = append(result, seg)
			continue
		}

		// Find local speed minima within the segment.
		troughs := findTroughs(smoothedSpeed, seg.start, seg.end)
		if len(troughs) < 2 {
			result = append(result, seg)
			continue
		}

		// Find split points: between consecutive troughs, if the intervening
		// speed peak rises by at least splitReaccelMPS above both troughs.
		splitPoints := findSplitPoints(smoothedSpeed, troughs, splitReaccelMPS)
		if len(splitPoints) == 0 {
			result = append(result, seg)
			continue
		}

		// Split the segment at each split point.
		prev := seg.start
		for _, sp := range splitPoints {
			part := rawSeg{
				isCorner: true,
				start:    prev,
				end:      sp - 1,
				latSign:  avgSign(signAvg, prev, sp-1),
			}
			result = append(result, part)
			prev = sp
		}
		// Final part.
		part := rawSeg{
			isCorner: true,
			start:    prev,
			end:      seg.end,
			latSign:  avgSign(signAvg, prev, seg.end),
		}
		result = append(result, part)
	}

	// Re-run chicane detection and mergeShort on the new segment list since we
	// may have created short segments or new chicane patterns.
	minStraightBuckets := max(1, int(0.012*numBuckets))
	minCornerBuckets := max(1, int(0.006*numBuckets))
	result = mergeShort(result, minStraightBuckets, minCornerBuckets)
	result = mergeChicanes(result, trackLengthM)

	return result
}

// findTroughs returns bucket indices within [start, end] that are local speed
// minima. A trough at bucket i means speedAvg[i] ≤ speedAvg[i-1] and
// speedAvg[i] ≤ speedAvg[i+1]. The first and last buckets in the range are
// not considered (need neighbors on both sides).
func findTroughs(speedAvg []float64, start, end int) []int {
	var troughs []int
	for i := start + 1; i < end; i++ {
		if speedAvg[i] <= speedAvg[i-1] && speedAvg[i] <= speedAvg[i+1] {
			// Skip plateaus: only record the first bucket in a run of equal values.
			if len(troughs) > 0 && i == troughs[len(troughs)-1]+1 && speedAvg[i] == speedAvg[troughs[len(troughs)-1]] {
				continue
			}
			troughs = append(troughs, i)
		}
	}
	return troughs
}

// findSplitPoints returns bucket indices where the speed profile between
// consecutive troughs rises by at least reaccelMPS above both neighboring
// troughs. The split point is the bucket of peak speed between the troughs.
func findSplitPoints(speedAvg []float64, troughs []int, reaccelMPS float64) []int {
	var splits []int
	for t := 0; t+1 < len(troughs); t++ {
		lo, hi := troughs[t], troughs[t+1]
		// Find peak speed between the two troughs.
		peakIdx := lo
		peakSpd := speedAvg[lo]
		for i := lo + 1; i <= hi; i++ {
			if speedAvg[i] > peakSpd {
				peakSpd = speedAvg[i]
				peakIdx = i
			}
		}
		// Check if the peak is significantly above both troughs.
		if peakSpd-speedAvg[lo] >= reaccelMPS && peakSpd-speedAvg[hi] >= reaccelMPS {
			splits = append(splits, peakIdx)
		}
	}
	return splits
}
