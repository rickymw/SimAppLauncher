package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// focusTestResult has three segments with per-segment rows for each, so a filter
// that silently kept everything would be visible.
func focusTestResult() analyzeResult {
	res := coachTestResult()
	res.TrackMap = &jsonTrackMap{
		Segments: []trackmap.Segment{
			{Name: "S1", Kind: trackmap.KindStraight},
			{Name: "T1", Kind: trackmap.KindCorner},
			{Name: "T2", Kind: trackmap.KindCorner},
		},
		GeoMethod:  "latlon",
		Confidence: "high",
	}
	res.AnalysedLap = &jsonAnalysedLap{
		Number: 4, LapTime: 101.5, TimeFormatted: "1:41.500", Selection: "best",
		Phases: []jsonPhase{
			{Segment: "S1", Phase: "full"},
			{Segment: "T1", Phase: "entry"},
			{Segment: "T1", Phase: "exit"},
			{Segment: "T2", Phase: "full"},
		},
		VsPB: []jsonPhaseDelta{
			{Segment: "T1", Phase: "entry"},
			{Segment: "T2", Phase: "full"},
		},
		ExitImpact: []jsonExitImpact{
			{Corner: "T1", Straight: "S1"},
			{Corner: "T2", Straight: "S1"},
		},
	}
	res.Consistency = []jsonConsistency{
		{Segment: "T1", Phase: "entry", Laps: 2},
		{Segment: "T2", Phase: "full", Laps: 2},
	}
	res.Notes = []jsonNote{{Text: "understeer on entry", Located: true, Segment: "T2"}}
	return res
}

func TestFocusOnSegments_FiltersPerSegmentRows(t *testing.T) {
	res, focus, err := focusOnSegments(focusTestResult(), "T1")
	if err != nil {
		t.Fatalf("focusOnSegments error: %v", err)
	}

	if len(focus) != 1 || focus[0] != "T1" {
		t.Errorf("focus = %v, want [T1]", focus)
	}
	if len(res.Focus) != 1 || res.Focus[0] != "T1" {
		t.Errorf("res.Focus = %v, want [T1] — the document must record that it was narrowed", res.Focus)
	}

	if got := len(res.AnalysedLap.Phases); got != 2 {
		t.Errorf("kept %d phases, want 2 (T1 entry and exit)", got)
	}
	for _, p := range res.AnalysedLap.Phases {
		if p.Segment != "T1" {
			t.Errorf("phase for %q survived a T1 focus", p.Segment)
		}
	}
	if got := len(res.AnalysedLap.VsPB); got != 1 || res.AnalysedLap.VsPB[0].Segment != "T1" {
		t.Errorf("vsPB = %v, want the T1 row alone", res.AnalysedLap.VsPB)
	}
	if got := len(res.AnalysedLap.ExitImpact); got != 1 || res.AnalysedLap.ExitImpact[0].Corner != "T1" {
		t.Errorf("exitImpact = %v, want the T1 row alone", res.AnalysedLap.ExitImpact)
	}
	if got := len(res.Consistency); got != 1 || res.Consistency[0].Segment != "T1" {
		t.Errorf("consistency = %v, want the T1 row alone", res.Consistency)
	}
}

// Session-level content is the context that makes one corner interpretable, so
// narrowing must not touch it.
func TestFocusOnSegments_KeepsSessionLevelContent(t *testing.T) {
	before := focusTestResult()
	res, _, err := focusOnSegments(before, "T1")
	if err != nil {
		t.Fatalf("focusOnSegments error: %v", err)
	}

	if len(res.Laps) != len(before.Laps) {
		t.Errorf("lap list changed: %d → %d", len(before.Laps), len(res.Laps))
	}
	if res.Sectors == nil {
		t.Error("sector times were dropped — they are how the corner's cost is judged")
	}
	if res.PB == nil {
		t.Error("PB header was dropped")
	}
	if len(res.Notes) != len(before.Notes) {
		t.Errorf("voice notes changed: %d → %d — the driver's words stay whole",
			len(before.Notes), len(res.Notes))
	}
	if res.TrackMap == nil || len(res.TrackMap.Segments) != 3 {
		t.Error("segment geometry was filtered; -table needs the full list to classify corners")
	}
}

// Filtering must not write through to the caller's slices.
func TestFocusOnSegments_DoesNotMutateInput(t *testing.T) {
	before := focusTestResult()
	if _, _, err := focusOnSegments(before, "T1"); err != nil {
		t.Fatalf("focusOnSegments error: %v", err)
	}
	if got := len(before.AnalysedLap.Phases); got != 4 {
		t.Errorf("input phases now %d, want the original 4", got)
	}
	if got := len(before.Consistency); got != 2 {
		t.Errorf("input consistency now %d, want the original 2", got)
	}
	if len(before.Focus) != 0 {
		t.Errorf("input Focus set to %v", before.Focus)
	}
}

func TestFocusOnSegments_MultipleSegments(t *testing.T) {
	// Given out of track order; the result should come back in it.
	res, focus, err := focusOnSegments(focusTestResult(), "T2,S1")
	if err != nil {
		t.Fatalf("focusOnSegments error: %v", err)
	}
	if len(focus) != 2 || focus[0] != "S1" || focus[1] != "T2" {
		t.Errorf("focus = %v, want [S1 T2] in track order", focus)
	}
	if got := len(res.AnalysedLap.Phases); got != 2 {
		t.Errorf("kept %d phases, want 2", got)
	}
}

func TestFocusOnSegments_Errors(t *testing.T) {
	if _, _, err := focusOnSegments(focusTestResult(), "T9"); err == nil {
		t.Error("expected an error naming an unknown segment")
	} else if !strings.Contains(err.Error(), "available") {
		t.Errorf("error should list the available segments, got: %v", err)
	}

	noMap := focusTestResult()
	noMap.TrackMap = nil
	if _, _, err := focusOnSegments(noMap, "T1"); err == nil {
		t.Error("expected an error when there is no track map to name corners against")
	}

	emptyMap := focusTestResult()
	emptyMap.TrackMap = &jsonTrackMap{}
	if _, _, err := focusOnSegments(emptyMap, "T1"); err == nil {
		t.Error("expected an error when the track map carries no segments")
	}
}

// The disclosure is what stops a narrowed brief being read as a whole-session
// one. Without it the assistant would rank one corner against corners it was
// never shown.
func TestBuildCoachBrief_FocusIsDisclosed(t *testing.T) {
	res, focus, err := focusOnSegments(focusTestResult(), "T1")
	if err != nil {
		t.Fatalf("focusOnSegments error: %v", err)
	}
	out := buildCoachBrief(trimForCoaching(res), "", focus, nil)

	for _, want := range []string{"Focus: T1", "removed", "do not"} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q from the focus disclosure:\n%s", want, out)
		}
	}
	// It belongs in the orientation, before the reader meets any numbers.
	if strings.Index(out, "Focus: T1") > strings.Index(out, "# Session data") {
		t.Error("focus disclosure should precede the session data")
	}
}

// An unfocused brief must not gain a disclosure claiming it was narrowed.
func TestBuildCoachBrief_NoFocusLineWhenWhole(t *testing.T) {
	out := buildCoachBrief(coachTestResult(), "", nil, nil)
	if strings.Contains(out, "Focus:") {
		t.Errorf("whole-session brief claims a focus:\n%s", out)
	}
}

func traceFixture() analysis.SegmentTrace {
	return analysis.SegmentTrace{
		Segment: "T1", Kind: "corner", SegmentIndex: 1,
		RateHz: 60, ContextSeconds: 1,
		Laps:    []int{3, 4},
		Columns: "Lap,Dist%,Time,Speed",
		Rows:    []string{"3,0.5000,0.000,142.0", "4,0.5000,0.000,139.5"},
	}
}

func TestBuildCoachBrief_RendersTraces(t *testing.T) {
	out := buildCoachBrief(coachTestResult(), "", []string{"T1"}, []analysis.SegmentTrace{traceFixture()})

	for _, want := range []string{
		"# Sample-level traces",
		"## T1 (corner) — laps 3, 4 at 60Hz",
		"```csv",
		"Lap,Dist%,Time,Speed",
		"3,0.5000,0.000,142.0",
		"4,0.5000,0.000,139.5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q:\n%s", want, out)
		}
	}

	// Traces follow the JSON payload: the aggregates frame the samples.
	if strings.Index(out, "# Sample-level traces") < strings.Index(out, "# Session data") {
		t.Error("traces should come after the session data")
	}
}

// A downsampled trace must say so, because ABS and Coast stop being point
// samples at that point.
func TestBuildCoachBrief_DownsampledTraceIsDisclosed(t *testing.T) {
	tr := traceFixture()
	tr.RateHz = 20
	out := buildCoachBrief(coachTestResult(), "", []string{"T1"}, []analysis.SegmentTrace{tr})

	if !strings.Contains(out, "Downsampled from 60Hz") {
		t.Errorf("downsampled trace not disclosed:\n%s", out)
	}
	if !strings.Contains(out, "set anywhere in the window") {
		t.Errorf("aggregation of ABS/Coast not explained:\n%s", out)
	}

	full := buildCoachBrief(coachTestResult(), "", []string{"T1"}, []analysis.SegmentTrace{traceFixture()})
	if strings.Contains(full, "Downsampled from") {
		t.Error("60Hz trace should not claim to be downsampled")
	}
}

// The JSON block must stay parseable with traces alongside it — they are
// rendered outside it precisely so they cannot corrupt it.
func TestBuildCoachBrief_JSONStillParsesWithTraces(t *testing.T) {
	out := buildCoachBrief(coachTestResult(), "", []string{"T1"}, []analysis.SegmentTrace{traceFixture()})

	start := strings.Index(out, "```json")
	if start < 0 {
		t.Fatal("no JSON block")
	}
	body := out[start+len("```json"):]
	end := strings.Index(body, "```")
	if end < 0 {
		t.Fatal("unterminated JSON block")
	}
	var res analyzeResult
	if err := json.Unmarshal([]byte(body[:end]), &res); err != nil {
		t.Fatalf("embedded JSON does not parse: %v", err)
	}
}
