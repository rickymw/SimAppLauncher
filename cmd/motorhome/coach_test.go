package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

func coachTestResult() analyzeResult {
	return analyzeResult{
		Schema:      analyzeSchema,
		File:        "session.ibt",
		Driver:      "Ricky Maw",
		Car:         "Porsche 718 GT4",
		Track:       "Phillip Island",
		SessionDate: "2026-07-29T19:48:50-07:00",
		Laps: []jsonLap{
			{Number: 1, Kind: "out lap"},
			{Number: 2, Kind: "flying lap", Complete: true},
			{Number: 3, Kind: "flying lap", Complete: true, Comparable: true},
			{Number: 4, Kind: "flying lap", Complete: true, Comparable: true},
		},
		TrackMap: &jsonTrackMap{
			Segments:   []trackmap.Segment{{Name: "S1"}, {Name: "T1"}},
			GeoMethod:  "latlon",
			Confidence: "high",
		},
		AnalysedLap: &jsonAnalysedLap{
			Number: 4, LapTime: 101.5, TimeFormatted: "1:41.500", Selection: "best",
		},
		PB: &jsonPB{
			LapTime: 100.0, LapTimeFormatted: "1:40.000", Date: "2026-06-21", DeltaToBest: 1.5,
		},
		Consistency: []jsonConsistency{{Segment: "T1", Phase: "entry", Laps: 2}},
		Sectors:     &jsonSectors{},
	}
}

func TestBuildCoachBrief_Structure(t *testing.T) {
	out := buildCoachBrief(coachTestResult(), "FRAMEWORK BODY HERE")

	for _, want := range []string{
		"# Coaching brief — Porsche 718 GT4 at Phillip Island",
		"Top 3 Actions",
		"## Session",
		"# Framework",
		"FRAMEWORK BODY HERE",
		"# Session data",
		"```json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q:\n%s", want, out)
		}
	}

	// The framework has to precede the data — the reader needs to know how to
	// read the numbers before meeting them.
	if strings.Index(out, "# Framework") > strings.Index(out, "# Session data") {
		t.Error("framework should come before the session data")
	}
}

// The embedded JSON must be valid and complete — it is the entire payload the
// coach reasons over.
func TestBuildCoachBrief_EmbeddedJSONParses(t *testing.T) {
	out := buildCoachBrief(coachTestResult(), "")

	start := strings.Index(out, "```json")
	if start < 0 {
		t.Fatalf("no JSON fence in brief:\n%s", out)
	}
	body := out[start+len("```json"):]
	end := strings.Index(body, "```")
	if end < 0 {
		t.Fatalf("unterminated JSON fence")
	}

	var back analyzeResult
	if err := json.Unmarshal([]byte(body[:end]), &back); err != nil {
		t.Fatalf("embedded JSON does not parse: %v", err)
	}
	if back.Car != "Porsche 718 GT4" || back.AnalysedLap == nil || back.AnalysedLap.Number != 4 {
		t.Errorf("embedded JSON lost content: %+v", back)
	}
}

func TestBuildCoachBrief_Orientation(t *testing.T) {
	out := buildCoachBrief(coachTestResult(), "")

	for _, want := range []string{
		"session.ibt",
		"Ricky Maw",
		"4 total, 3 flying, 2 comparable",
		"lap 4 (1:41.500, selected as best)",
		"1:40.000",
		"1.500s off it",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("orientation missing %q:\n%s", want, out)
		}
	}
}

// A session that beat the stored PB must read as beating it, not as being a
// negative distance off it.
func TestBuildCoachBrief_NewPBReadsAsFaster(t *testing.T) {
	res := coachTestResult()
	res.PB.DeltaToBest = -0.42

	out := buildCoachBrief(res, "")
	if !strings.Contains(out, "beat it by 0.420s") {
		t.Errorf("expected a beat-the-PB phrasing:\n%s", out)
	}
	if strings.Contains(out, "-0.420s off") {
		t.Errorf("negative delta rendered as 'off it':\n%s", out)
	}
}

// Absent data has to be named. Coaching around a missing track map without
// realising it is missing produces confident findings about nothing.
func TestBuildCoachBrief_NamesGaps(t *testing.T) {
	res := coachTestResult()
	res.TrackMap = nil
	res.Consistency = nil
	res.PB = nil
	res.Sectors = nil

	out := buildCoachBrief(res, "")
	for _, want := range []string{"no track map", "no consistency data", "no stored PB", "no sector times"} {
		if !strings.Contains(out, want) {
			t.Errorf("gap %q not reported:\n%s", want, out)
		}
	}
}

func TestBuildCoachBrief_NoGapsLineWhenComplete(t *testing.T) {
	out := buildCoachBrief(coachTestResult(), "")
	if strings.Contains(out, "- Gaps:") {
		t.Errorf("complete session should have no Gaps line:\n%s", out)
	}
}

func TestBuildCoachBrief_MentionsVoiceNotes(t *testing.T) {
	res := coachTestResult()
	res.Notes = []jsonNote{{Text: "loose on exit", Located: true, Lap: 3, Segment: "T1"}}

	out := buildCoachBrief(res, "")
	if !strings.Contains(out, "Voice notes: 1") {
		t.Errorf("expected the notes count in the orientation:\n%s", out)
	}
}

func TestBuildCoachBrief_NoFramework(t *testing.T) {
	out := buildCoachBrief(coachTestResult(), "")
	if strings.Contains(out, "# Framework") {
		t.Errorf("empty framework should not produce a Framework heading:\n%s", out)
	}
	// The data still has to be there.
	if !strings.Contains(out, "# Session data") {
		t.Errorf("data section missing:\n%s", out)
	}
}

func TestBuildCoachBrief_CutLapsCounted(t *testing.T) {
	res := coachTestResult()
	res.Laps = append(res.Laps, jsonLap{Number: 5, Kind: "flying lap", Complete: true, Cut: true})

	out := buildCoachBrief(res, "")
	if !strings.Contains(out, "1 cut") {
		t.Errorf("cut laps should be surfaced:\n%s", out)
	}
}

// ---- trimming ----

// Segment geometry is pure position data; every segment is already named in
// the phase rows, so carrying 17 sets of coordinates is payload with no
// coaching signal.
func TestTrimForCoaching_DropsSegmentGeometry(t *testing.T) {
	res := coachTestResult()
	trimmed := trimForCoaching(res)

	if trimmed.TrackMap.Segments != nil {
		t.Error("segment geometry should be dropped from the coach brief")
	}
	if trimmed.TrackMap.SegmentCount != 2 {
		t.Errorf("SegmentCount = %d, want 2", trimmed.TrackMap.SegmentCount)
	}
	// Map quality must survive: a low-confidence map means findings pinned to
	// segment boundaries need hedging.
	if trimmed.TrackMap.Confidence != "high" || trimmed.TrackMap.GeoMethod != "latlon" {
		t.Errorf("map metadata was lost: %+v", trimmed.TrackMap)
	}
}

// Trimming must not mutate the caller's struct — the same result is also the
// one -json would emit in full.
func TestTrimForCoaching_DoesNotMutateInput(t *testing.T) {
	res := coachTestResult()
	_ = trimForCoaching(res)

	if res.TrackMap.Segments == nil {
		t.Error("trimForCoaching mutated its input")
	}
	if len(res.TrackMap.Segments) != 2 {
		t.Errorf("input segments changed: %+v", res.TrackMap.Segments)
	}
}

func TestTrimForCoaching_NoTrackMap(t *testing.T) {
	res := coachTestResult()
	res.TrackMap = nil
	if trimmed := trimForCoaching(res); trimmed.TrackMap != nil {
		t.Error("expected no track map to survive as nil")
	}
}

// ---- framework loading ----

func TestCoachFrameworkText_ReadsFromConfigDir(t *testing.T) {
	dir := t.TempDir()
	want := "# Lap Coaching Instructions\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, coachFrameworkFile), []byte(want), 0644); err != nil {
		t.Fatal(err)
	}

	got := coachFrameworkText(false, filepath.Join(dir, "launcher.config.json"))
	if got != want {
		t.Errorf("coachFrameworkText = %q, want %q", got, want)
	}
}

func TestCoachFrameworkText_Omitted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, coachFrameworkFile), []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := coachFrameworkText(true, filepath.Join(dir, "launcher.config.json")); got != "" {
		t.Errorf("-no-framework should suppress the framework, got %q", got)
	}
}

// A missing framework must not be fatal — the data section still carries
// everything measured.
func TestCoachFrameworkText_MissingIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	got := coachFrameworkText(false, filepath.Join(dir, "launcher.config.json"))
	if got != "" {
		t.Errorf("expected empty framework when coach.md is absent, got %q", got)
	}
}

func TestCoachLapCounts(t *testing.T) {
	res := coachTestResult()
	res.Laps = append(res.Laps, jsonLap{Number: 5, Kind: "flying lap", Cut: true, Comparable: false})

	total, flying, comparable, cut := coachLapCounts(res)
	if total != 5 || flying != 4 || comparable != 2 || cut != 1 {
		t.Errorf("coachLapCounts = %d/%d/%d/%d, want 5/4/2/1", total, flying, comparable, cut)
	}
}
