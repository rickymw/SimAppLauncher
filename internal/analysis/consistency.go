package analysis

// Per-segment consistency across a set of laps. The phase table describes one
// lap; this describes the spread of the same metrics over every lap in the
// session, which is where repeatable lap time actually comes from.

import (
	"math"
	"sort"

	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// MinLapsForConsistency is the minimum number of laps a (segment, phase) pair
// must appear on before a spread can be reported for it. One observation has
// no spread, and printing "± 0.0" for it would read as perfect consistency
// rather than as absent data.
const MinLapsForConsistency = 2

// ConsistencyRow summarises how much one (segment, phase) pair varied across
// the laps it was measured on.
//
// Pairs are matched by segment name + phase kind, the same key the vs-PB table
// uses. A corner can be split entry/mid/exit on one lap and collapsed to a
// single "full" phase on another (peak steering below minPeakSteerDeg), so the
// phases present are not guaranteed to be identical from lap to lap — Laps
// records how many laps actually contributed to each row.
//
// SD values are the sample standard deviation (n-1 denominator).
type ConsistencyRow struct {
	SegIndex int
	SegName  string
	Kind     PhaseKind
	Laps     int

	EntrySpeedMean, EntrySpeedSD float32 // km/h
	ExitSpeedMean, ExitSpeedSD   float32 // km/h
	PeakBrakeMean, PeakBrakeSD   float32 // 0–100%
	LatGMean, LatGSD             float32 // g
	CoastMean, CoastSD           float32 // seconds

	BestExitSpeedKPH float32 // fastest exit speed seen across the laps
	BestExitLap      int     // lap number that set BestExitSpeedKPH
}

// phaseKindRank orders phases within a segment for display: the order a driver
// actually drives them.
func phaseKindRank(k PhaseKind) int {
	switch k {
	case PhaseEntry:
		return 0
	case PhaseMid:
		return 1
	case PhaseExit:
		return 2
	default: // PhaseFull
		return 3
	}
}

// consistencyAcc accumulates per-lap observations for one (segment, phase) pair.
type consistencyAcc struct {
	segIdx  int
	segName string
	kind    PhaseKind

	entry   []float32
	exit    []float32
	peakBrk []float32
	latG    []float32
	coast   []float32
	lapNums []int
}

// ComputeConsistency computes the per-(segment, phase) spread of key metrics
// across laps. Callers should pass an already-filtered set of comparable laps
// (flying, non-cut, within a sensible time of the best) — mixing an out lap in
// would dominate every spread it touches.
//
// Rows observed on fewer than MinLapsForConsistency laps are omitted. Returns
// nil when there is no track map or fewer than MinLapsForConsistency laps.
func ComputeConsistency(laps []Lap, segs []trackmap.Segment, brakeEntries pb.BrakeEntryMap) []ConsistencyRow {
	if len(segs) == 0 || len(laps) < MinLapsForConsistency {
		return nil
	}

	accs := map[string]*consistencyAcc{}
	var order []string

	for i := range laps {
		lap := &laps[i]
		for _, p := range ComputePhases(lap, segs, brakeEntries) {
			if p.SampleCount == 0 {
				continue
			}
			key := p.SegName + "|" + string(p.Kind)
			a := accs[key]
			if a == nil {
				a = &consistencyAcc{segIdx: p.SegIndex, segName: p.SegName, kind: p.Kind}
				accs[key] = a
				order = append(order, key)
			}
			a.entry = append(a.entry, p.SpeedEntryKPH)
			a.exit = append(a.exit, p.SpeedExitKPH)
			a.peakBrk = append(a.peakBrk, p.PeakBrakePct)
			a.latG = append(a.latG, p.LatGAvg)
			a.coast = append(a.coast, float32(p.CoastSamples)/60.0)
			a.lapNums = append(a.lapNums, lap.Number)
		}
	}

	// Track order, then the order the phases are driven in.
	sort.SliceStable(order, func(i, j int) bool {
		ai, aj := accs[order[i]], accs[order[j]]
		if ai.segIdx != aj.segIdx {
			return ai.segIdx < aj.segIdx
		}
		return phaseKindRank(ai.kind) < phaseKindRank(aj.kind)
	})

	var rows []ConsistencyRow
	for _, key := range order {
		a := accs[key]
		if len(a.entry) < MinLapsForConsistency {
			continue
		}
		row := ConsistencyRow{
			SegIndex: a.segIdx,
			SegName:  a.segName,
			Kind:     a.kind,
			Laps:     len(a.entry),
		}
		row.EntrySpeedMean, row.EntrySpeedSD = meanSD(a.entry)
		row.ExitSpeedMean, row.ExitSpeedSD = meanSD(a.exit)
		row.PeakBrakeMean, row.PeakBrakeSD = meanSD(a.peakBrk)
		row.LatGMean, row.LatGSD = meanSD(a.latG)
		row.CoastMean, row.CoastSD = meanSD(a.coast)

		row.BestExitLap = -1
		for i, v := range a.exit {
			if row.BestExitLap < 0 || v > row.BestExitSpeedKPH {
				row.BestExitSpeedKPH = v
				row.BestExitLap = a.lapNums[i]
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// MostVariable returns up to n rows ordered by descending exit-speed spread —
// the phases where lap-to-lap repeatability is worst, and therefore where the
// largest consistent gain is available.
//
// Exit speed is the ranking metric because it is the one that propagates: a
// corner exit that varies by 5 km/h carries that variance down the whole
// following straight.
func MostVariable(rows []ConsistencyRow, n int) []ConsistencyRow {
	if n <= 0 || len(rows) == 0 {
		return nil
	}
	sorted := make([]ConsistencyRow, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].ExitSpeedSD > sorted[j].ExitSpeedSD
	})
	if n > len(sorted) {
		n = len(sorted)
	}
	return sorted[:n]
}

// meanSD returns the mean and sample standard deviation (n-1 denominator) of v.
// SD is 0 for fewer than two values. Accumulation is in float64: summing a few
// dozen float32 speeds loses precision that shows up in the variance.
func meanSD(v []float32) (mean, sd float32) {
	if len(v) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range v {
		sum += float64(x)
	}
	m := sum / float64(len(v))
	if len(v) < 2 {
		return float32(m), 0
	}
	var ss float64
	for _, x := range v {
		d := float64(x) - m
		ss += d * d
	}
	return float32(m), float32(math.Sqrt(ss / float64(len(v)-1)))
}
