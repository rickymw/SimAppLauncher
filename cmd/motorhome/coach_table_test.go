package main

import (
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// coachTableResult is a two-corner session: T1 clean, T2 tripping every flag.
func coachTableResult() analyzeResult {
	f32 := func(v float32) *float32 { return &v }
	return analyzeResult{
		Schema: analyzeSchema,
		File:   "session.ibt",
		Car:    "Porsche 718 GT4",
		Track:  "Phillip Island",
		Laps: []jsonLap{
			{Number: 3, Kind: "flying lap", Complete: true, Comparable: true},
			{Number: 4, Kind: "flying lap", Complete: true, Comparable: true},
		},
		TrackMap: &jsonTrackMap{
			Segments: []trackmap.Segment{
				{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0},
				{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.1},
				{Name: "S2", Kind: trackmap.KindStraight, EntryPct: 0.2},
				{Name: "T2 Honda", Kind: trackmap.KindCorner, EntryPct: 0.6},
			},
		},
		AnalysedLap: &jsonAnalysedLap{
			Number: 4, LapTime: 101.5, TimeFormatted: "1:41.500", Selection: "best",
			Phases: []jsonPhase{
				{Segment: "S1", Phase: "full", SpeedEntryKPH: 220, SpeedExitKPH: 200, Lockups: 600},
				{Segment: "T1", Phase: "entry", SpeedEntryKPH: 200, SpeedExitKPH: 150},
				{Segment: "T1", Phase: "exit", SpeedEntryKPH: 150, SpeedExitKPH: 180},
				{Segment: "S2", Phase: "full", SpeedEntryKPH: 180, SpeedExitKPH: 210},
				{
					Segment: "T2 Honda", Phase: "entry", SpeedEntryKPH: 210, SpeedExitKPH: 80,
					CoastSeconds: 1.0, Lockups: 120, Corrections: 2,
				},
				{
					Segment: "T2 Honda", Phase: "exit", SpeedEntryKPH: 80, SpeedExitKPH: 95,
					Wheelspin: 90, Corrections: 1,
				},
			},
		},
		Consistency: []jsonConsistency{
			{Segment: "T1", Phase: "entry", Laps: 2, ExitSpeedSD: 1.0, BestExitKPH: 151, BestExitLap: 3},
			{Segment: "T2 Honda", Phase: "entry", Laps: 2, ExitSpeedSD: 25.0, BestExitKPH: 99, BestExitLap: 3},
			{Segment: "T2 Honda", Phase: "exit", Laps: 2, ExitSpeedSD: 4.0, BestExitKPH: 97, BestExitLap: 4},
		},
		Sectors: &jsonSectors{
			StartPct: []float32{0, 0.5},
			PerLap: []jsonSectorLap{
				{Lap: 3, Times: []*float32{f32(51.0), f32(51.0)}, LapTime: 102.0},
				{Lap: 4, Times: []*float32{f32(50.5), f32(51.0)}, LapTime: 101.5},
			},
			Best:        []float32{50.5, 50.0},
			BestFromLap: []int{4, 3},
			Theoretical: f32(100.5),
		},
	}
}

func TestBuildCoachTurnRows_AggregatesPhasesPerCorner(t *testing.T) {
	rows := buildCoachTurnRows(coachTableResult())

	if len(rows) != 2 {
		t.Fatalf("want 2 corner rows (straights dropped), got %d: %+v", len(rows), rows)
	}
	if rows[0].Name != "T1" || rows[1].Name != "T2 Honda" {
		t.Fatalf("rows out of track order: %s, %s", rows[0].Name, rows[1].Name)
	}

	honda := rows[1]
	if honda.SpeedIn != 210 || honda.SpeedMin != 80 || honda.SpeedOut != 95 {
		t.Errorf("speed in>min>out = %v>%v>%v, want 210>80>95",
			honda.SpeedIn, honda.SpeedMin, honda.SpeedOut)
	}
	if honda.Coast != 1.0 {
		t.Errorf("coast = %v, want 1.0 (summed across phases)", honda.Coast)
	}
	if honda.Lock != 2.0 { // 120 samples / 60 Hz
		t.Errorf("lock = %v, want 2.0s", honda.Lock)
	}
	if honda.Spin != 1.5 { // 90 samples / 60 Hz
		t.Errorf("spin = %v, want 1.5s", honda.Spin)
	}
	if honda.Corr != 3 {
		t.Errorf("corrections = %d, want 3 (summed)", honda.Corr)
	}
	// Worst phase, not the mean: 25.0 from entry, not (25+4)/2.
	if honda.ExitSD != 25.0 {
		t.Errorf("exitSD = %v, want 25.0 (worst phase)", honda.ExitSD)
	}
}

// The straight carries 600 lockup samples; charging them to a corner would
// make an adjacent corner look far worse than it was driven.
func TestBuildCoachTurnRows_StraightsExcluded(t *testing.T) {
	for _, r := range buildCoachTurnRows(coachTableResult()) {
		if r.Name == "S1" || r.Name == "S2" {
			t.Fatalf("straight %q leaked into the turn table", r.Name)
		}
		if r.Lock >= 10 {
			t.Fatalf("%s absorbed the straight's lockups: %v s", r.Name, r.Lock)
		}
	}
}

func TestGradeCoachTurn_FlagsAndLetters(t *testing.T) {
	tests := []struct {
		name      string
		row       coachTurnRow
		wantFlags []string
		wantGrade string
	}{
		{"clean", coachTurnRow{Laps: 4}, nil, "A"},
		{"coast only", coachTurnRow{Coast: 1.0, Laps: 4}, []string{"coast"}, "B"},
		{
			"everything",
			coachTurnRow{Coast: 2, Lock: 3, Spin: 2, ExitSD: 30, Corr: 5, Laps: 4},
			[]string{"coast", "lock", "spin", "spread", "corr"},
			"F",
		},
		// At the threshold, not over it.
		{"exactly at cutoff", coachTurnRow{Coast: coachCoastFlagSeconds, Laps: 4}, nil, "A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, grade := gradeCoachTurn(tt.row)
			if grade != tt.wantGrade {
				t.Errorf("grade = %q, want %q (flags %v)", grade, tt.wantGrade, flags)
			}
			if strings.Join(flags, " ") != strings.Join(tt.wantFlags, " ") {
				t.Errorf("flags = %v, want %v", flags, tt.wantFlags)
			}
		})
	}
}

// One lap has no spread to measure. Reporting that as a clean corner would be
// a lie of omission, so the spread flag must not fire.
func TestGradeCoachTurn_SpreadNeedsTwoLaps(t *testing.T) {
	flags, grade := gradeCoachTurn(coachTurnRow{ExitSD: 40, Laps: 1})
	if len(flags) != 0 || grade != "A" {
		t.Errorf("single-lap corner tripped spread: flags=%v grade=%s", flags, grade)
	}
	if flags, _ := gradeCoachTurn(coachTurnRow{ExitSD: 40, Laps: 2}); len(flags) != 1 {
		t.Errorf("two-lap corner should trip spread, got %v", flags)
	}
}

func TestWriteCoachTable_RendersRowsLegendAndSectors(t *testing.T) {
	var b strings.Builder
	writeCoachTable(&b, coachTableResult())
	out := b.String()

	for _, want := range []string{
		"Turn-by-turn — lap 4 (1:41.500)",
		"T2 Honda",
		"210>80>95",
		"coast lock spin spread", // corr is 3 -> also flagged, checked below
		"Flags trip at:",
		"triage hint, not a measurement",
		"Sector loss",
		"Theoretical best 1:40.500",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(out, "| F\n") {
		t.Errorf("T2 Honda should grade F (5 flags)\n---\n%s", out)
	}
}

// The Covers column names the corners in each sector, which is what makes a
// sector number actionable.
func TestWriteCoachTable_SectorCoversNamesCorners(t *testing.T) {
	var b strings.Builder
	writeCoachTable(&b, coachTableResult())
	out := b.String()

	if !strings.Contains(out, "T1") || !strings.Contains(out, "T2 Honda") {
		t.Errorf("sector spans missing corner names\n---\n%s", out)
	}
	if !strings.Contains(out, "0.500") { // lap 4 S2 51.0 vs best 50.0
		t.Errorf("sector loss not computed\n---\n%s", out)
	}
}

// trimForCoaching drops segment geometry; the table must still render rather
// than silently reporting no corners.
func TestBuildCoachTurnRows_FallsBackWithoutGeometry(t *testing.T) {
	res := trimForCoaching(coachTableResult())
	rows := buildCoachTurnRows(res)

	if len(rows) != 2 {
		t.Fatalf("want 2 corners from the phase-name fallback, got %d: %+v", len(rows), rows)
	}
	if rows[1].Name != "T2 Honda" {
		t.Errorf("row 1 = %q, want T2 Honda", rows[1].Name)
	}
}

func TestWriteCoachTable_NoTrackMap(t *testing.T) {
	res := coachTableResult()
	res.TrackMap = nil
	res.AnalysedLap.Phases = nil

	var b strings.Builder
	writeCoachTable(&b, res)
	if !strings.Contains(b.String(), "no track map") {
		t.Errorf("want an explanation when there are no segments, got:\n%s", b.String())
	}
}

func TestWriteCoachMostVariable_RanksWorstFirst(t *testing.T) {
	var b strings.Builder
	writeCoachMostVariable(&b, coachTableResult())
	out := b.String()

	honda := strings.Index(out, "T2 Honda entry")
	t1 := strings.Index(out, "T1 entry")
	if honda < 0 {
		t.Fatalf("worst-spread row missing:\n%s", out)
	}
	if t1 >= 0 && t1 < honda {
		t.Errorf("rows not sorted by spread descending:\n%s", out)
	}
	if !strings.Contains(out, "best 99.0 km/h on lap 3") {
		t.Errorf("missing best-exit attribution:\n%s", out)
	}
}

// Single-lap sessions have no consistency rows at all; the section should be
// omitted rather than printing an empty heading.
func TestWriteCoachMostVariable_OmittedWithoutSpread(t *testing.T) {
	res := coachTableResult()
	res.Consistency = nil

	var b strings.Builder
	writeCoachMostVariable(&b, res)
	if b.String() != "" {
		t.Errorf("want no output without consistency data, got:\n%s", b.String())
	}
}

func TestBuildCoachTableView_IncludesOrientation(t *testing.T) {
	out := buildCoachTableView(coachTableResult())

	if !strings.Contains(out, "## Session") {
		t.Errorf("table view must keep the orientation:\n%s", out)
	}
	if strings.Contains(out, "```json") {
		t.Errorf("table view must not emit the JSON payload:\n%s", out)
	}
}
