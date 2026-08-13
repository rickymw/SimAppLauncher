package main

// -trace: sample-level telemetry for named segments, carried inline rather than
// written to a CSV file the way -dump is.
//
// The two are not redundant. -dump produces a file to plot or hand to something
// else; -trace produces rows that travel with the analysis, which is what lets
// `coach -segment T3` put the corner's samples in front of the assistant that is
// doing the coaching. Both share DumpConfig and writeDumpRows, so the numbers
// and their formatting are identical.

import (
	"fmt"
	"os"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// buildSegmentTraces resolves the -trace segment list and builds one trace per
// segment, in track order.
//
// Traces cover every comparable lap rather than only the analysed one. Comparing
// the laps against each other is the point of zooming in: a single lap's trace
// shows what the driver did, but not which of the things they did was the one
// that varied. The analysed lap alone is the fallback for a session with no
// comparable set (a single flying lap, typically).
func buildSegmentTraces(spec string, segs []trackmap.Segment, laps, comparableLaps []analysis.Lap,
	lapNum, hz int) []analysis.SegmentTrace {

	if len(segs) == 0 {
		analyzeDie("-trace requires a track map (run analyze once first to auto-detect segments)")
	}
	idxs, err := analysis.ResolveSegmentList(segs, spec)
	if err != nil {
		analyzeDie("%v — available: %s", err, segmentNames(segs))
	}

	cfg := analysis.DefaultTraceConfig()
	if hz > 0 {
		cfg.DownsampleRate = analysis.DownsampleRateForHz(hz)
	}

	traceLaps := comparableLaps
	if len(traceLaps) == 0 {
		lap := selectAnalyzeLap(laps, lapNum)
		if lap == nil {
			analyzeDie("-trace found no lap to trace")
		}
		traceLaps = []analysis.Lap{*lap}
	}

	var out []analysis.SegmentTrace
	for _, idx := range idxs {
		tr, err := analysis.BuildSegmentTrace(traceLaps, segs, idx, cfg)
		if err != nil {
			// One corner missing from every lap does not invalidate the others;
			// warn and carry on rather than failing the whole run.
			fmt.Fprintf(os.Stderr, "Warning: -trace %s: %v\n", segs[idx].Name, err)
			continue
		}
		out = append(out, tr)
	}
	reportTraceSize(out)
	return out
}

// traceRowsWarnThreshold is where a trace stops being a detail and starts being
// the bulk of what it is attached to. Measured against a real session: one long
// corner complex across 5 comparable laps at 60Hz runs to ~3,900 rows, three
// times the size of the entire unfocused brief.
const traceRowsWarnThreshold = 2000

// reportTraceSize tells the user on stderr when a trace is large enough that the
// rate is worth reconsidering.
//
// 60Hz is the right default — it is the point of focusing — but the cost is
// invisible until something downstream chokes on it, and the fix is one flag.
// Since binary channels are now aggregated rather than decimated
// (binaryFlagsOverWindow), dropping to 20Hz no longer loses lock-ups: it costs
// resolution on the continuous traces and nothing else.
func reportTraceSize(traces []analysis.SegmentTrace) {
	rows, bytes := 0, 0
	minHz := analysis.SampleRateHz
	for _, tr := range traces {
		rows += len(tr.Rows)
		for _, r := range tr.Rows {
			bytes += len(r) + 1
		}
		if tr.RateHz < minHz {
			minHz = tr.RateHz
		}
	}
	if rows < traceRowsWarnThreshold || minHz <= 20 {
		return
	}
	fmt.Fprintf(os.Stderr,
		"Note: %d trace rows at %dHz (~%d KB). -hz 20 gives roughly a third of that; "+
			"ABS and lock-ups still survive the downsampling.\n",
		rows, minHz, bytes/1024)
}

// printTraces renders the traces to the analyze sink as CSV blocks.
func printTraces(traces []analysis.SegmentTrace) {
	for _, tr := range traces {
		aprintf("Trace: %s (%s) — %d laps (%s) at %dHz, %.1fs context each side\n",
			tr.Segment, tr.Kind, len(tr.Laps), intsJoin(tr.Laps), tr.RateHz, tr.ContextSeconds)
		if tr.RateHz < analysis.SampleRateHz {
			aprintf("  ABS and Coast are 1 if set anywhere in the window a row covers.\n")
		}
		aprintf("%s\n", tr.Columns)
		for _, row := range tr.Rows {
			aprintf("%s\n", row)
		}
		aprintln()
	}
}

// intsJoin renders a lap-number list as "3, 4, 6".
func intsJoin(ns []int) string {
	out := ""
	for i, n := range ns {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprint(n)
	}
	return out
}
