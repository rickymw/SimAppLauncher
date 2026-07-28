package analysis

import (
	"strconv"
	"strings"
)

// Sector is one timing sector as published by iRacing in the session YAML's
// SplitTimeInfo block. StartPct is the sector's start position as a fraction
// of a lap (LapDistPct); the sector runs until the next sector's StartPct, or
// to 1.0 for the last one.
type Sector struct {
	Num      int
	StartPct float32
}

// SectorTime is the time spent in one sector on a particular lap.
// Complete is false when the lap's samples do not span the whole sector — an
// out lap, a partial-start lap, or a recording that stopped mid-lap — in which
// case Seconds is meaningless and callers should render it as unavailable.
type SectorTime struct {
	Num      int
	StartPct float32
	EndPct   float32
	Seconds  float32
	Complete bool
}

// ParseSectors extracts sector boundaries from the session YAML's SplitTimeInfo
// block:
//
//	SplitTimeInfo:
//	 Sectors:
//	 - SectorNum: 0
//	   SectorStartPct: 0.000000
//	 - SectorNum: 1
//	   SectorStartPct: 0.143086
//
// These are iRacing's own sector definitions, so they match the timing in the
// UI. Returns nil when the block is absent (some session types omit it).
// Sectors are returned in ascending StartPct order.
func ParseSectors(yaml string) []Sector {
	idx := strings.Index(yaml, "\nSplitTimeInfo:\n")
	start := idx + 1
	if idx < 0 {
		if !strings.HasPrefix(yaml, "SplitTimeInfo:\n") {
			return nil
		}
		start = 0
	}

	var sectors []Sector
	var cur Sector
	haveNum := false

	lines := strings.Split(yaml[start:], "\n")
	// Skip the "SplitTimeInfo:" header, then read while indented — an
	// unindented line marks the next top-level key.
	for _, line := range lines[1:] {
		if len(line) == 0 {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			break
		}
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "- ")

		switch {
		case strings.HasPrefix(trimmed, "SectorNum:"):
			// A new SectorNum starts a new record; flush any half-built one.
			if haveNum {
				sectors = append(sectors, cur)
			}
			cur = Sector{}
			haveNum = true
			if n, err := strconv.Atoi(strings.TrimSpace(trimmed[len("SectorNum:"):])); err == nil {
				cur.Num = n
			}
		case strings.HasPrefix(trimmed, "SectorStartPct:"):
			if f, err := strconv.ParseFloat(strings.TrimSpace(trimmed[len("SectorStartPct:"):]), 32); err == nil {
				cur.StartPct = float32(f)
			}
		}
	}
	if haveNum {
		sectors = append(sectors, cur)
	}
	if len(sectors) == 0 {
		return nil
	}
	return sectors
}

// ComputeSectorTimes returns the time spent in each sector on lap.
//
// Boundary crossing times are linearly interpolated between the samples either
// side of the boundary rather than snapped to the nearest sample: at 60 Hz,
// snapping would introduce up to 17 ms of error per boundary, which is visible
// against lap times quoted to a millisecond.
func ComputeSectorTimes(lap *Lap, sectors []Sector) []SectorTime {
	if lap == nil || len(sectors) == 0 || len(lap.Samples) < 2 {
		return nil
	}

	out := make([]SectorTime, len(sectors))
	for i, s := range sectors {
		endPct := float32(1.0)
		if i+1 < len(sectors) {
			endPct = sectors[i+1].StartPct
		}
		out[i] = SectorTime{Num: s.Num, StartPct: s.StartPct, EndPct: endPct}
	}

	for i := range out {
		startT, okStart := crossingTime(lap, out[i].StartPct)
		endT, okEnd := crossingTime(lap, out[i].EndPct)
		if !okStart || !okEnd || endT <= startT {
			continue
		}
		// SessionTime is absolute seconds since session start and can be large,
		// so the subtraction is done in float64 before narrowing.
		out[i].Seconds = float32(endT - startT)
		out[i].Complete = true
	}
	return out
}

// crossingTime returns the SessionTime at which the lap crosses pct, linearly
// interpolated between the bracketing samples. The boundaries at 0.0 and 1.0
// map to the first and last sample, since a lap's samples never span exactly
// those values.
func crossingTime(lap *Lap, pct float32) (float64, bool) {
	samples := lap.Samples
	if len(samples) < 2 {
		return 0, false
	}
	if pct <= samples[0].LapDistPct {
		return samples[0].SessionTime, true
	}
	last := samples[len(samples)-1]
	if pct >= last.LapDistPct {
		return last.SessionTime, true
	}

	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1], samples[i]
		// Skip backwards steps (S/F wrap or noise) rather than interpolating
		// across them, which would yield a nonsense time.
		if cur.LapDistPct < prev.LapDistPct {
			continue
		}
		if cur.LapDistPct >= pct && prev.LapDistPct <= pct {
			span := cur.LapDistPct - prev.LapDistPct
			if span <= 0 {
				return cur.SessionTime, true
			}
			frac := float64((pct - prev.LapDistPct) / span)
			return prev.SessionTime + frac*(cur.SessionTime-prev.SessionTime), true
		}
	}
	return 0, false
}

// BestSectorTimes returns, for each sector index, the fastest time across laps
// plus the lap number it came from. Sectors no lap completed report Complete
// false. The sum of the best sectors is the theoretical best lap.
func BestSectorTimes(perLap [][]SectorTime, lapNumbers []int) ([]SectorTime, []int) {
	if len(perLap) == 0 {
		return nil, nil
	}
	n := 0
	for _, st := range perLap {
		if len(st) > n {
			n = len(st)
		}
	}
	if n == 0 {
		return nil, nil
	}

	best := make([]SectorTime, n)
	from := make([]int, n)
	for i := range from {
		from[i] = -1
	}
	for li, st := range perLap {
		for i := 0; i < len(st) && i < n; i++ {
			if !st[i].Complete {
				continue
			}
			if !best[i].Complete || st[i].Seconds < best[i].Seconds {
				best[i] = st[i]
				if li < len(lapNumbers) {
					from[i] = lapNumbers[li]
				}
			}
		}
	}
	return best, from
}
