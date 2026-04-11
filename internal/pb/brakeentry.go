// Package pb — brakeentry.go defines per-corner brake onset types and helpers.
// Brake entries are stored inside PersonalBest.BrakeEntries (part of pb.json)
// rather than a separate file, since they are keyed by the same car/track pair.
package pb

// BrakeEntry holds the weighted-average lap-distance fraction at which the
// driver begins braking for one corner, plus the number of laps that have
// contributed to the average (used for weighted blending across sessions).
type BrakeEntry struct {
	Pct      float32 `json:"pct"`      // LapDistPct of brake onset (0.0–1.0)
	LapsUsed int     `json:"lapsUsed"` // number of laps contributing to Pct
}

// BrakeEntryMap maps segment name → BrakeEntry for one car/track combination.
// Keys match trackmap.Segment.Name (e.g. "T1", "T2-3").
type BrakeEntryMap map[string]BrakeEntry

// BrakeEntryLookup returns the stored brake onset pct for a given car, track,
// and segment name. Returns (0, false) if not found.
func BrakeEntryLookup(pbf File, car, track, segName string) (float32, bool) {
	entry, ok := pbf[Key(car, track)]
	if !ok || entry == nil || entry.BrakeEntries == nil {
		return 0, false
	}
	be, ok := entry.BrakeEntries[segName]
	if !ok || be.Pct == 0 {
		return 0, false
	}
	return be.Pct, true
}

// BrakeEntrySet stores or updates a brake entry for a given car/track/segment,
// blending the new value with any existing stored value using a weighted average.
// newPct is the new measured onset; newLaps is the number of laps it represents.
// If no PersonalBest entry exists for the car/track yet, a stub is created so
// that brake entries can accumulate independently of PB lap times.
func BrakeEntrySet(pbf File, car, track, segName string, newPct float32, newLaps int) {
	key := Key(car, track)
	if pbf[key] == nil {
		pbf[key] = &PersonalBest{Car: car, Track: track}
	}
	pb := pbf[key]
	if pb.BrakeEntries == nil {
		pb.BrakeEntries = make(BrakeEntryMap)
	}
	existing := pb.BrakeEntries[segName]
	if existing.Pct == 0 || existing.LapsUsed == 0 {
		pb.BrakeEntries[segName] = BrakeEntry{Pct: newPct, LapsUsed: newLaps}
		return
	}
	// Weighted average blends old and new measurements.
	total := existing.LapsUsed + newLaps
	blended := (existing.Pct*float32(existing.LapsUsed) + newPct*float32(newLaps)) / float32(total)
	pb.BrakeEntries[segName] = BrakeEntry{Pct: blended, LapsUsed: total}
}
