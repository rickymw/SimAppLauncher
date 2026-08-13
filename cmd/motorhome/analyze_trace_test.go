package main

import (
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

func traceTestSegs() []trackmap.Segment {
	return []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}
}

// traceTestLap spans both segments of traceTestSegs.
func traceTestLap(number int, speed float32) analysis.Lap {
	var samples []analysis.SampleData
	const n = 120
	for i := 0; i < n; i++ {
		samples = append(samples, analysis.SampleData{
			LapDistPct:  float32(i) / float32(n),
			SessionTime: float64(number*1000) + float64(i)/60.0,
			Speed:       speed,
			Throttle:    0.8,
			Gear:        3,
		})
	}
	return analysis.Lap{Number: number, LapTime: 92.0, Kind: analysis.KindFlying, Samples: samples}
}

func TestBuildSegmentTraces_UsesComparableLaps(t *testing.T) {
	laps := []analysis.Lap{traceTestLap(3, 50), traceTestLap(4, 51), traceTestLap(5, 52)}
	comparable := []analysis.Lap{laps[0], laps[2]}

	traces := buildSegmentTraces("T1", traceTestSegs(), laps, comparable, 0, 0)

	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if traces[0].Segment != "T1" {
		t.Errorf("segment = %q, want T1", traces[0].Segment)
	}
	// Comparing the laps against each other is the point of zooming in, so the
	// trace must span the comparable set, not just the analysed lap.
	if len(traces[0].Laps) != 2 || traces[0].Laps[0] != 3 || traces[0].Laps[1] != 5 {
		t.Errorf("laps = %v, want [3 5] (the comparable set)", traces[0].Laps)
	}
	if traces[0].RateHz != 60 {
		t.Errorf("RateHz = %d, want 60 — focused traces default to full rate", traces[0].RateHz)
	}
}

// With no comparable set (a single flying lap) the analysed lap alone is better
// than nothing.
func TestBuildSegmentTraces_FallsBackToAnalysedLap(t *testing.T) {
	laps := []analysis.Lap{traceTestLap(3, 50), traceTestLap(4, 51)}

	traces := buildSegmentTraces("T1", traceTestSegs(), laps, nil, 4, 0)

	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if len(traces[0].Laps) != 1 || traces[0].Laps[0] != 4 {
		t.Errorf("laps = %v, want [4] (the explicitly analysed lap)", traces[0].Laps)
	}
}

func TestBuildSegmentTraces_MultipleSegmentsInTrackOrder(t *testing.T) {
	laps := []analysis.Lap{traceTestLap(3, 50)}

	traces := buildSegmentTraces("T1,S1", traceTestSegs(), laps, laps, 0, 0)

	if len(traces) != 2 {
		t.Fatalf("got %d traces, want 2", len(traces))
	}
	if traces[0].Segment != "S1" || traces[1].Segment != "T1" {
		t.Errorf("segments = %q, %q; want S1 then T1 (track order)",
			traces[0].Segment, traces[1].Segment)
	}
}

func TestBuildSegmentTraces_HzOverride(t *testing.T) {
	laps := []analysis.Lap{traceTestLap(3, 50)}

	full := buildSegmentTraces("T1", traceTestSegs(), laps, laps, 0, 0)
	slow := buildSegmentTraces("T1", traceTestSegs(), laps, laps, 0, 20)

	if slow[0].RateHz != 20 {
		t.Errorf("RateHz = %d, want 20", slow[0].RateHz)
	}
	// A third of the rate should be about a third of the rows.
	if len(slow[0].Rows) >= len(full[0].Rows) {
		t.Errorf("20Hz produced %d rows, 60Hz produced %d — expected fewer",
			len(slow[0].Rows), len(full[0].Rows))
	}
}

func TestPrintTraces(t *testing.T) {
	tr := analysis.SegmentTrace{
		Segment: "T1", Kind: "corner", RateHz: 60, ContextSeconds: 1,
		Laps:    []int{3, 5},
		Columns: "Lap,Dist%,Time,Speed",
		Rows:    []string{"3,0.5000,0.000,142.0", "5,0.5000,0.000,139.5"},
	}

	out := captureAnalyzeOut(t, func() { printTraces([]analysis.SegmentTrace{tr}) })

	for _, want := range []string{
		"Trace: T1 (corner) — 2 laps (3, 5) at 60Hz",
		"Lap,Dist%,Time,Speed",
		"3,0.5000,0.000,142.0",
		"5,0.5000,0.000,139.5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printTraces output missing %q:\n%s", want, out)
		}
	}
	// At full rate there is nothing to disclaim about ABS/Coast.
	if strings.Contains(out, "anywhere in the window") {
		t.Errorf("60Hz trace should not carry the aggregation note:\n%s", out)
	}
}

func TestPrintTraces_DownsampledCarriesNote(t *testing.T) {
	tr := analysis.SegmentTrace{
		Segment: "T1", Kind: "corner", RateHz: 20,
		Laps: []int{3}, Columns: "Lap,Dist%", Rows: []string{"3,0.5000"},
	}
	out := captureAnalyzeOut(t, func() { printTraces([]analysis.SegmentTrace{tr}) })
	if !strings.Contains(out, "anywhere in the window") {
		t.Errorf("downsampled trace missing the aggregation note:\n%s", out)
	}
}

func TestPrintTraces_EmptyPrintsNothing(t *testing.T) {
	if out := captureAnalyzeOut(t, func() { printTraces(nil) }); out != "" {
		t.Errorf("printTraces(nil) wrote %q", out)
	}
}

func TestIntsJoin(t *testing.T) {
	cases := []struct {
		in   []int
		want string
	}{
		{nil, ""},
		{[]int{4}, "4"},
		{[]int{3, 5, 12}, "3, 5, 12"},
	}
	for _, c := range cases {
		if got := intsJoin(c.in); got != c.want {
			t.Errorf("intsJoin(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
