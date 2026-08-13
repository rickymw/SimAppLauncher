package analysis

// Sample-level segment traces carried in-process, as opposed to the -dump CSVs
// written to disk.
//
// The dump exists so a human (or an assistant with a file open) can plot a
// corner. A trace exists so the *coaching brief itself* can carry the corner:
// aggregate rows say an exit is 3 km/h slower and varies by 8, but only the
// samples say the throttle came in 0.4s later on the lap that lost the time.
// Building it here rather than re-reading the .ibt keeps `coach` on the single
// analyze pipeline, which is what stops the two from drifting.

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// SegmentTrace is one segment's telemetry across one or more laps.
//
// Rows are pre-formatted CSV lines rather than structured numbers. That is a
// deliberate concession to the consumer: the trace's whole purpose is to be read
// by a language model inside a brief, and an indent-encoded array of objects
// costs several times the tokens of the same numbers as CSV while being harder
// to scan. Columns names them, and the formatting comes from the same
// writeDumpRows the CSV dump uses, so the two cannot disagree.
type SegmentTrace struct {
	Segment      string `json:"segment"`
	Kind         string `json:"kind"`
	SegmentIndex int    `json:"segmentIndex"`
	// RateHz is the rate rows were actually emitted at, which may differ from
	// the one requested — see DownsampleRateForHz.
	RateHz         int     `json:"rateHz"`
	ContextSeconds float32 `json:"contextSeconds"`
	// Laps names the laps that contributed rows, in the order they appear.
	Laps    []int    `json:"laps"`
	Columns string   `json:"columns"`
	Rows    []string `json:"rows"`
}

// BuildSegmentTrace collects segIdx's telemetry from every lap in laps.
//
// The row format matches DumpSegmentAllLapsCSV exactly, including the leading
// Lap column and the per-lap clock restart — the traces are meant to be overlaid
// at equal time-into-the-corner, which a session-relative clock would prevent.
// It is used even for a single lap so the column set does not change shape with
// the lap count.
//
// Laps with no samples in the segment are skipped, matching the CSV dump: a
// truncated lap can legitimately miss a corner. An error is returned only when
// no lap yielded any rows.
func BuildSegmentTrace(laps []Lap, segs []trackmap.Segment, segIdx int, cfg DumpConfig) (SegmentTrace, error) {
	if segIdx < 0 || segIdx >= len(segs) {
		return SegmentTrace{}, fmt.Errorf("segment index %d out of range (0–%d)", segIdx, len(segs)-1)
	}
	if len(laps) == 0 {
		return SegmentTrace{}, fmt.Errorf("no laps to trace")
	}
	if cfg.DownsampleRate < 1 {
		cfg.DownsampleRate = 1
	}

	seg := segs[segIdx]
	effEntry, effExit := segmentEffBounds(segs)

	out := SegmentTrace{
		Segment:        seg.Name,
		Kind:           string(seg.Kind),
		SegmentIndex:   segIdx,
		RateHz:         cfg.OutputHz(),
		ContextSeconds: float32(cfg.ContextSamples) / float32(SampleRateHz),
		Columns:        "Lap," + dumpColumns,
	}

	var buf bytes.Buffer
	for i := range laps {
		lap := &laps[i]
		start, end, ok := segmentSampleRange(lap, effEntry, effExit, segIdx, cfg.ContextSamples)
		if !ok {
			continue
		}
		buf.Reset()
		writeDumpRows(&buf, lap.Samples[start:end+1], cfg, fmt.Sprintf("%d,", lap.Number))
		rows := splitTraceRows(buf.String())
		if len(rows) == 0 {
			continue
		}
		out.Laps = append(out.Laps, lap.Number)
		out.Rows = append(out.Rows, rows...)
	}

	if len(out.Rows) == 0 {
		return SegmentTrace{}, fmt.Errorf("no samples found in segment %s on any of the %d laps",
			seg.Name, len(laps))
	}
	return out, nil
}

// splitTraceRows turns writeDumpRows' newline-terminated output into one string
// per row, dropping the trailing empty element.
func splitTraceRows(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// ResolveSegmentList resolves a comma-separated list of segment names or 1-based
// indices ("T3", "T3,T4", "3,4") to segment indices in track order.
//
// Track order rather than the order given: everything else in the output is
// presented in track order, and a caller who wrote "T7,T3" almost certainly
// meant "these two corners", not "in this sequence". Duplicates collapse.
//
// An unrecognised entry fails the whole list rather than being skipped. A typo
// that silently traced fewer corners than asked for would be read as "that
// corner had no data", which is a different and much more misleading answer.
func ResolveSegmentList(segs []trackmap.Segment, spec string) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := ResolveSegmentName(segs, part)
		if idx < 0 {
			return nil, fmt.Errorf("segment %q not found", part)
		}
		if seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, idx)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no segments named")
	}
	sort.Ints(out)
	return out, nil
}
