package analysis

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// SampleRateHz is the tick rate every dump and trace assumes .ibt telemetry was
// recorded at. iRacing writes 60Hz; the real rate is on the file header, but the
// sample-count-derived figures elsewhere in this package have assumed 60 since
// they were introduced and changing that here would silently move them.
const SampleRateHz = 60

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

// DefaultTraceConfig returns the config used when the caller has already
// narrowed the output to a named corner or two.
//
// Full 60Hz, where DefaultDumpConfig settles for 20Hz. The 20Hz figure buys
// token headroom that a focused trace does not need — one corner across a
// handful of laps at 60Hz is smaller than a whole session at 20Hz — and the
// detail it costs (the exact frame a brake release or lock-up starts) is
// precisely what someone zooming into one corner is looking for.
func DefaultTraceConfig() DumpConfig {
	return DumpConfig{
		DownsampleRate: 1,  // full 60Hz
		ContextSamples: 60, // 1 second at 60Hz
	}
}

// DownsampleRateForHz converts a requested output rate into a sample stride.
//
// Rates that do not divide SampleRateHz evenly are rounded to the nearest whole
// stride, so the emitted rate can differ from the one asked for. Callers should
// report OutputHz rather than the request.
func DownsampleRateForHz(hz int) int {
	if hz < 1 {
		return 1
	}
	if hz >= SampleRateHz {
		return 1
	}
	// Round to nearest rather than truncating: -hz 25 should give 30Hz
	// (stride 2), not 20Hz (stride 3).
	rate := (SampleRateHz + hz/2) / hz
	if rate < 1 {
		return 1
	}
	return rate
}

// OutputHz reports the rate rows are actually emitted at.
func (c DumpConfig) OutputHz() int {
	if c.DownsampleRate < 1 {
		return SampleRateHz
	}
	return SampleRateHz / c.DownsampleRate
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

		end := i + cfg.DownsampleRate
		if end > len(samples) {
			end = len(samples)
		}
		abs, coast := binaryFlagsOverWindow(samples[i:end])

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

// binaryFlagsOverWindow reduces the ABS and coast flags across every sample a
// single output row stands for, taking the maximum rather than the value at the
// row's own sample.
//
// Plain decimation is right for continuous channels — speed read every third
// sample is still speed — but ABS and coast are 0/1 events. Taking every third
// one discards two thirds of each ABS activation, and a lock-up lasting a few
// frames can disappear from the trace entirely. That is exactly the signal
// someone zooming into a problem corner is looking for, so the row reports
// whether the event occurred anywhere in the window it represents.
//
// At DownsampleRate 1 the window is a single sample and this is a no-op, so the
// full-rate output is unchanged.
func binaryFlagsOverWindow(win []SampleData) (abs, coast int) {
	for _, s := range win {
		if s.ABSActive {
			abs = 1
		}
		if s.Throttle < 0.05 && s.Brake < 0.05 {
			coast = 1
		}
	}
	return abs, coast
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
	writeDumpRateHeader(w, cfg)

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
	writeDumpRateHeader(w, cfg)
	fmt.Fprintln(w, "# Time restarts at 0 for each lap so the traces can be overlaid.")

	fmt.Fprintln(w, "Lap,"+dumpColumns)
	for _, win := range windows {
		writeDumpRows(w, win.samples, cfg, fmt.Sprintf("%d,", win.lap.Number))
	}
	return nil
}

// writeDumpRateHeader writes the rate/context comment lines shared by both dump
// modes, disclosing that ABS and Coast are maxima over the window rather than
// point samples — otherwise a reader would reasonably assume every column was
// read at the same instant.
func writeDumpRateHeader(w io.Writer, cfg DumpConfig) {
	fmt.Fprintf(w, "# Rate: %dHz (downsampled from %dHz)\n", cfg.OutputHz(), SampleRateHz)
	fmt.Fprintf(w, "# Context: %d samples before/after segment boundary\n", cfg.ContextSamples/cfg.DownsampleRate)
	if cfg.DownsampleRate > 1 {
		fmt.Fprintf(w, "# ABS and Coast are 1 if set anywhere in the %d-sample window a row covers.\n",
			cfg.DownsampleRate)
	}
}

// lapListSummary renders "3 (1:24.512), 5 (1:24.331)" for a dump header.
func lapListSummary(laps []*Lap) string {
	parts := make([]string, len(laps))
	for i, l := range laps {
		parts[i] = fmt.Sprintf("%d (%s)", l.Number, FormatLapTime(l.LapTime))
	}
	return strings.Join(parts, ", ")
}
