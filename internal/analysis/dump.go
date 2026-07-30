package analysis

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// DumpConfig controls CSV dump output.
type DumpConfig struct {
	// DownsampleRate controls how many 60Hz samples to skip between output rows.
	// 3 = 20Hz output (every 3rd sample). 1 = full 60Hz.
	DownsampleRate int
	// ContextSamples is the number of extra samples to include before and after
	// the segment boundary (at the original 60Hz rate, before downsampling).
	ContextSamples int
}

// DefaultDumpConfig returns a config tuned for AI token efficiency:
// 20Hz output with 1 second of context on each side.
func DefaultDumpConfig() DumpConfig {
	return DumpConfig{
		DownsampleRate: 3,  // 60Hz → 20Hz
		ContextSamples: 60, // 1 second at 60Hz
	}
}

// ResolveSegmentName finds a segment by name (case-insensitive) or 1-based
// index string (e.g. "3" → segment index 2). Returns the segment index or -1.
func ResolveSegmentName(segs []trackmap.Segment, nameOrIdx string) int {
	// Try as 1-based index first.
	if idx := parseInt1Based(nameOrIdx); idx >= 0 && idx < len(segs) {
		return idx
	}
	// Case-insensitive name match.
	lower := strings.ToLower(nameOrIdx)
	for i, seg := range segs {
		if strings.ToLower(seg.Name) == lower {
			return i
		}
	}
	return -1
}

// parseInt1Based parses a string as a 1-based integer and returns the 0-based
// index. Returns -1 if the string is not a valid positive integer.
func parseInt1Based(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return -1
	}
	return n - 1
}

// dumpColumns is the shared telemetry column set written by both dump modes.
//
// Units: Speed km/h, Throttle/Brake 0–100, Steer degrees (+=left),
// LatG/LongG in g, ABS/Coast 0/1, Time seconds from the first row of that lap's
// window.
const dumpColumns = "Dist%,Time,Speed,Throttle,Brake,Steer,Gear,LatG,LongG,ABS,Coast"

// segmentEffBounds returns the effective entry/exit boundaries for every
// segment, clamping each segment's exit to the following segment's entry
// (the same rule ComputePhases applies).
//
// Unlike ComputePhases this uses the geometric entry directly: a dump is meant
// to show the raw approach to a corner, so shifting the window to the stored
// brake onset would hide the part the reader most wants to see.
func segmentEffBounds(segs []trackmap.Segment) (effEntry, effExit []float32) {
	effEntry = make([]float32, len(segs))
	effExit = make([]float32, len(segs))
	for i, s := range segs {
		effEntry[i] = s.EntryPct
		effExit[i] = s.ExitPct
	}
	for i := 0; i < len(segs)-1; i++ {
		if effEntry[i+1] < effExit[i] {
			effExit[i] = effEntry[i+1]
		}
	}
	return effEntry, effExit
}

// segmentSampleRange returns the inclusive sample index range covering segIdx
// on lap, widened by ctx samples of lead-in and lead-out and clamped to the
// lap. ok is false when the lap has no samples in that segment.
func segmentSampleRange(lap *Lap, effEntry, effExit []float32, segIdx, ctx int) (start, end int, ok bool) {
	segStart, segEnd := -1, -1
	for i, s := range lap.Samples {
		if segmentForEffPct(s.LapDistPct, effEntry, effExit) == segIdx {
			if segStart < 0 {
				segStart = i
			}
			segEnd = i
		}
	}
	if segStart < 0 {
		return 0, 0, false
	}

	start = segStart - ctx
	if start < 0 {
		start = 0
	}
	end = segEnd + ctx
	if end >= len(lap.Samples) {
		end = len(lap.Samples) - 1
	}
	return start, end, true
}

// writeDumpRows writes samples as CSV rows at cfg.DownsampleRate, prefixing
// each row with prefix (used to carry the lap number in multi-lap dumps).
// Times are relative to the first sample in samples.
func writeDumpRows(w io.Writer, samples []SampleData, cfg DumpConfig, prefix string) {
	t0 := samples[0].SessionTime
	for i := 0; i < len(samples); i += cfg.DownsampleRate {
		s := samples[i]

		abs := 0
		if s.ABSActive {
			abs = 1
		}
		coast := 0
		if s.Throttle < 0.05 && s.Brake < 0.05 {
			coast = 1
		}

		fmt.Fprintf(w, "%s%.4f,%.3f,%.1f,%.0f,%.0f,%.1f,%d,%.2f,%.2f,%d,%d\n",
			prefix,
			s.LapDistPct,
			s.SessionTime-t0,
			s.Speed*ms2kmh,
			s.Throttle*100,
			s.Brake*100,
			s.SteeringAngle*rad2deg,
			s.Gear,
			s.LatAccel/grav,
			s.LongAccel/grav,
			abs,
			coast,
		)
	}
}

// DumpSegmentCSV writes a downsampled CSV of telemetry for a single segment
// to w. It includes ContextSamples of lead-in and lead-out from adjacent
// segments so the AI can see the approach and exit.
//
// The CSV has a comment header with segment metadata, then the dumpColumns
// column set.
func DumpSegmentCSV(w io.Writer, lap *Lap, segs []trackmap.Segment, segIdx int, cfg DumpConfig) error {
	if segIdx < 0 || segIdx >= len(segs) {
		return fmt.Errorf("segment index %d out of range (0–%d)", segIdx, len(segs)-1)
	}
	if cfg.DownsampleRate < 1 {
		cfg.DownsampleRate = 1
	}

	seg := segs[segIdx]
	effEntry, effExit := segmentEffBounds(segs)

	start, end, ok := segmentSampleRange(lap, effEntry, effExit, segIdx, cfg.ContextSamples)
	if !ok {
		return fmt.Errorf("no samples found in segment %s", seg.Name)
	}
	samples := lap.Samples[start : end+1]

	fmt.Fprintf(w, "# Segment: %s (%s)\n", seg.Name, seg.Kind)
	fmt.Fprintf(w, "# Lap: %d, Time: %s\n", lap.Number, FormatLapTime(lap.LapTime))
	fmt.Fprintf(w, "# Rate: %dHz (downsampled from 60Hz)\n", 60/cfg.DownsampleRate)
	fmt.Fprintf(w, "# Context: %d samples before/after segment boundary\n", cfg.ContextSamples/cfg.DownsampleRate)

	fmt.Fprintln(w, dumpColumns)
	writeDumpRows(w, samples, cfg, "")
	return nil
}

// DumpSegmentAllLapsCSV writes the same segment from every lap in laps into a
// single CSV, prefixed with a Lap column so the traces can be overlaid.
//
// Time restarts at 0 for each lap's window rather than running continuously
// through the session. That is the point of the multi-lap dump: the rows are
// meant to be compared against each other at equal time-into-the-corner, which
// a session-relative clock would make impossible without further arithmetic.
//
// Laps with no samples in the segment are skipped rather than failing the whole
// dump — a lap can legitimately miss a segment (recording started late, or the
// lap was truncated). An error is returned only when no lap yielded any rows.
func DumpSegmentAllLapsCSV(w io.Writer, laps []Lap, segs []trackmap.Segment, segIdx int, cfg DumpConfig) error {
	if segIdx < 0 || segIdx >= len(segs) {
		return fmt.Errorf("segment index %d out of range (0–%d)", segIdx, len(segs)-1)
	}
	if len(laps) == 0 {
		return fmt.Errorf("no laps to dump")
	}
	if cfg.DownsampleRate < 1 {
		cfg.DownsampleRate = 1
	}

	seg := segs[segIdx]
	effEntry, effExit := segmentEffBounds(segs)

	// Resolve every lap's window first so the header can name exactly the laps
	// that made it into the file.
	type lapWindow struct {
		lap     *Lap
		samples []SampleData
	}
	var windows []lapWindow
	for i := range laps {
		lap := &laps[i]
		start, end, ok := segmentSampleRange(lap, effEntry, effExit, segIdx, cfg.ContextSamples)
		if !ok {
			continue
		}
		windows = append(windows, lapWindow{lap: lap, samples: lap.Samples[start : end+1]})
	}
	if len(windows) == 0 {
		return fmt.Errorf("no samples found in segment %s on any of the %d laps", seg.Name, len(laps))
	}

	dumped := make([]*Lap, len(windows))
	for i, win := range windows {
		dumped[i] = win.lap
	}

	fmt.Fprintf(w, "# Segment: %s (%s)\n", seg.Name, seg.Kind)
	fmt.Fprintf(w, "# Laps: %d (%s)\n", len(windows), lapListSummary(dumped))
	fmt.Fprintf(w, "# Rate: %dHz (downsampled from 60Hz)\n", 60/cfg.DownsampleRate)
	fmt.Fprintf(w, "# Context: %d samples before/after segment boundary\n", cfg.ContextSamples/cfg.DownsampleRate)
	fmt.Fprintln(w, "# Time restarts at 0 for each lap so the traces can be overlaid.")

	fmt.Fprintln(w, "Lap,"+dumpColumns)
	for _, win := range windows {
		writeDumpRows(w, win.samples, cfg, fmt.Sprintf("%d,", win.lap.Number))
	}
	return nil
}

// lapListSummary renders "3 (1:24.512), 5 (1:24.331)" for a dump header.
func lapListSummary(laps []*Lap) string {
	parts := make([]string, len(laps))
	for i, l := range laps {
		parts[i] = fmt.Sprintf("%d (%s)", l.Number, FormatLapTime(l.LapTime))
	}
	return strings.Join(parts, ", ")
}
