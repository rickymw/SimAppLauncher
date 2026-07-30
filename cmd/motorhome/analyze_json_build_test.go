package main

import (
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// jsonBuildInput assembles a realistic analyzeResultInput from the shared
// output-test fixtures.
func jsonBuildInput() analyzeResultInput {
	laps := []analysis.Lap{outputTestLap(2, 102.0), outputTestLap(3, 101.5)}
	outLap := outputTestLap(1, 130)
	outLap.Kind = analysis.KindOutLap
	all := append([]analysis.Lap{outLap}, laps...)

	return analyzeResultInput{
		ibtPath:        "D:\\telemetry\\session.ibt",
		meta:           analysis.SessionMeta{DriverName: "Ricky Maw", CarScreenName: "Porsche 718 GT4", TrackDisplayName: "Watkins Glen"},
		sessionDate:    time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
		sampleCount:    22325,
		tickRate:       60,
		laps:           all,
		comparableLaps: laps,
		segs:           outputTestSegs(),
		sectors:        []analysis.Sector{{Num: 1, StartPct: 0.0}, {Num: 2, StartPct: 0.5}},
	}
}

func TestBuildAnalyzeResult_Header(t *testing.T) {
	res := buildAnalyzeResult(jsonBuildInput())

	if res.Schema != analyzeSchema {
		t.Errorf("Schema = %q, want %q", res.Schema, analyzeSchema)
	}
	// The path is reduced to a basename: the absolute path is machine-specific
	// and not useful to a downstream consumer.
	if res.File != "session.ibt" {
		t.Errorf("File = %q, want session.ibt", res.File)
	}
	if res.Car != "Porsche 718 GT4" || res.Track != "Watkins Glen" || res.Driver != "Ricky Maw" {
		t.Errorf("header fields wrong: %+v", res)
	}
	if res.Samples != 22325 || res.TickRateHz != 60 {
		t.Errorf("Samples/TickRateHz = %d/%d", res.Samples, res.TickRateHz)
	}
	if !strings.HasPrefix(res.SessionDate, "2026-07-30") {
		t.Errorf("SessionDate = %q", res.SessionDate)
	}
}

// The comparable flag is what tells a consumer which laps the cross-lap views
// were computed from; it must not simply mirror "is a flying lap".
func TestBuildAnalyzeResult_MarksComparableLaps(t *testing.T) {
	res := buildAnalyzeResult(jsonBuildInput())

	if len(res.Laps) != 3 {
		t.Fatalf("expected 3 laps, got %d", len(res.Laps))
	}
	byNumber := map[int]jsonLap{}
	for _, l := range res.Laps {
		byNumber[l.Number] = l
	}
	if byNumber[1].Comparable {
		t.Error("out lap should not be marked comparable")
	}
	if !byNumber[2].Comparable || !byNumber[3].Comparable {
		t.Error("flying laps in the comparable set should be marked")
	}
	if byNumber[1].Kind != "out lap" {
		t.Errorf("lap 1 Kind = %q", byNumber[1].Kind)
	}
	if byNumber[3].TimeFormatted != "1:41.500" {
		t.Errorf("lap 3 TimeFormatted = %q", byNumber[3].TimeFormatted)
	}
}

// An incomplete lap has no time, and emitting 0 would read as an instant lap.
func TestBuildAnalyzeResult_IncompleteLapHasNoTime(t *testing.T) {
	in := jsonBuildInput()
	partial := outputTestLap(4, 0)
	in.laps = append(in.laps, partial)

	res := buildAnalyzeResult(in)
	for _, l := range res.Laps {
		if l.Number != 4 {
			continue
		}
		if l.Complete || l.LapTime != 0 || l.TimeFormatted != "" {
			t.Errorf("incomplete lap should carry no time: %+v", l)
		}
	}
}

func TestBuildAnalyzeResult_TrackMap(t *testing.T) {
	in := jsonBuildInput()
	in.trackMap = &trackmap.TrackMap{
		GeoMethod: "latlon", LapsUsed: 47, SessionsUsed: 6,
	}
	in.matchScore = 0.94
	in.geomConf = trackmap.ConfHigh

	res := buildAnalyzeResult(in)
	if res.TrackMap == nil {
		t.Fatal("TrackMap should be present when segments exist")
	}
	if res.TrackMap.GeoMethod != "latlon" || res.TrackMap.LapsUsed != 47 || res.TrackMap.SessionsUsed != 6 {
		t.Errorf("track map fields wrong: %+v", res.TrackMap)
	}
	if res.TrackMap.Confidence != "high" {
		t.Errorf("Confidence = %q, want high", res.TrackMap.Confidence)
	}
	if res.TrackMap.MatchScore == nil || *res.TrackMap.MatchScore != 0.94 {
		t.Errorf("MatchScore = %v, want 0.94", res.TrackMap.MatchScore)
	}
}

// "No comparable lap to score against" and "scored 0" are different facts, so
// an absent score must be nil rather than zero.
func TestBuildAnalyzeResult_AbsentMatchScoreIsNil(t *testing.T) {
	in := jsonBuildInput()
	in.matchScore = -1

	res := buildAnalyzeResult(in)
	if res.TrackMap == nil {
		t.Fatal("TrackMap should still be present")
	}
	if res.TrackMap.MatchScore != nil {
		t.Errorf("MatchScore should be nil when not computed, got %v", *res.TrackMap.MatchScore)
	}
}

func TestBuildAnalyzeResult_NoSegmentsOmitsTrackMap(t *testing.T) {
	in := jsonBuildInput()
	in.segs = nil

	res := buildAnalyzeResult(in)
	if res.TrackMap != nil {
		t.Errorf("TrackMap should be omitted with no segments: %+v", res.TrackMap)
	}
	// The zone fallback stands in for phases when there is no map.
	if res.AnalysedLap == nil || len(res.AnalysedLap.Zones) == 0 {
		t.Error("expected the zone fallback when no track map exists")
	}
	if res.AnalysedLap != nil && len(res.AnalysedLap.Phases) > 0 {
		t.Error("phases should be absent without a track map")
	}
}

func TestBuildAnalyzeResult_PBDelta(t *testing.T) {
	in := jsonBuildInput()
	in.pbEntry = &pb.PersonalBest{
		LapTime: 100.0, LapTimeFormatted: "1:40.000", Date: "2026-06-21", Weather: "Track 35°C",
	}

	res := buildAnalyzeResult(in)
	if res.PB == nil {
		t.Fatal("PB should be present")
	}
	// Best lap this session is 101.5, PB is 100.0 → 1.5s off.
	if d := res.PB.DeltaToBest; d < 1.49 || d > 1.51 {
		t.Errorf("DeltaToBest = %v, want ~1.5", d)
	}
	if res.PB.LapTimeFormatted != "1:40.000" || res.PB.Weather != "Track 35°C" {
		t.Errorf("PB fields wrong: %+v", res.PB)
	}
}

// A stub entry created by BrakeEntrySet has LapTime 0 and is not a real PB.
func TestBuildAnalyzeResult_StubPBEntryOmitted(t *testing.T) {
	in := jsonBuildInput()
	in.pbEntry = &pb.PersonalBest{LapTime: 0}

	if res := buildAnalyzeResult(in); res.PB != nil {
		t.Errorf("a zero-time stub entry is not a PB: %+v", res.PB)
	}
}

func TestBuildAnalyzeResult_PhasesAndExitImpact(t *testing.T) {
	res := buildAnalyzeResult(jsonBuildInput())

	if res.AnalysedLap == nil {
		t.Fatal("AnalysedLap missing")
	}
	if res.AnalysedLap.Selection != "best" {
		t.Errorf("Selection = %q, want best", res.AnalysedLap.Selection)
	}
	if res.AnalysedLap.Number != 3 {
		t.Errorf("best lap should be 3, got %d", res.AnalysedLap.Number)
	}
	if len(res.AnalysedLap.Phases) == 0 {
		t.Error("expected phases")
	}
	if len(res.AnalysedLap.ExitImpact) == 0 {
		t.Error("expected exit impact pairs")
	}
	if res.AnalysedLap.Tyres == nil {
		t.Fatal("expected tyre data")
	}
	if _, ok := res.AnalysedLap.Tyres.Corners["LF"]; !ok {
		t.Errorf("tyre corners missing LF: %+v", res.AnalysedLap.Tyres.Corners)
	}
}

func TestBuildAnalyzeResult_ExplicitLapSelection(t *testing.T) {
	in := jsonBuildInput()
	in.lapNum = 2

	res := buildAnalyzeResult(in)
	if res.AnalysedLap.Selection != "explicit" || res.AnalysedLap.Number != 2 {
		t.Errorf("expected explicit lap 2, got %s lap %d",
			res.AnalysedLap.Selection, res.AnalysedLap.Number)
	}
}

func TestBuildAnalyzeResult_VsPB(t *testing.T) {
	in := jsonBuildInput()
	lap := outputTestLap(3, 101.5)
	phases := analysis.ComputePhases(&lap, in.segs, nil)
	in.pbPhases = phasesToPB(phases)

	res := buildAnalyzeResult(in)
	if len(res.AnalysedLap.VsPB) == 0 {
		t.Fatal("expected vs-PB deltas when stored phases match")
	}
	// Comparing the lap against its own phases must produce zero deltas.
	for _, d := range res.AnalysedLap.VsPB {
		if d.DSpeedEntryKPH != 0 || d.DCoastSeconds != 0 {
			t.Errorf("self-comparison should be all zeroes: %+v", d)
		}
	}
}

func TestBuildAnalyzeResult_Notes(t *testing.T) {
	in := jsonBuildInput()
	in.notes = []analysis.LocatedNote{
		{Text: "loose on exit", At: time.Now(), Located: true, LapNumber: 3, LapDistPct: 0.63, SegName: "T1"},
		{Text: "good session", At: time.Now(), Located: false},
	}

	res := buildAnalyzeResult(in)
	if len(res.Notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(res.Notes))
	}
	if !res.Notes[0].Located || res.Notes[0].Segment != "T1" || res.Notes[0].Lap != 3 {
		t.Errorf("located note wrong: %+v", res.Notes[0])
	}
	// An unplaced note keeps its text but must not claim a position.
	if res.Notes[1].Located || res.Notes[1].Lap != 0 || res.Notes[1].Segment != "" {
		t.Errorf("unlocated note should carry no position: %+v", res.Notes[1])
	}
	if res.Notes[1].Text != "good session" {
		t.Errorf("unlocated note lost its text: %+v", res.Notes[1])
	}
}

func TestBuildAnalyzeResult_Consistency(t *testing.T) {
	in := jsonBuildInput()
	in.consistency = analysis.ComputeConsistency(in.comparableLaps, in.segs, nil)
	if len(in.consistency) == 0 {
		t.Skip("fixture produced no consistency rows")
	}

	res := buildAnalyzeResult(in)
	if len(res.Consistency) != len(in.consistency) {
		t.Errorf("Consistency = %d rows, want %d", len(res.Consistency), len(in.consistency))
	}
	if res.Consistency[0].Segment == "" || res.Consistency[0].Laps == 0 {
		t.Errorf("consistency row not populated: %+v", res.Consistency[0])
	}
}

// ---- sectors ----

func TestBuildJSONSectors(t *testing.T) {
	laps := []analysis.Lap{outputTestLap(2, 102.0), outputTestLap(3, 101.5)}
	sectors := []analysis.Sector{{Num: 1, StartPct: 0.0}, {Num: 2, StartPct: 0.5}}

	got := buildJSONSectors(laps, sectors)
	if got == nil {
		t.Fatal("expected sector data")
	}
	if len(got.StartPct) != 2 || got.StartPct[1] != 0.5 {
		t.Errorf("StartPct = %v", got.StartPct)
	}
	if len(got.PerLap) != 2 {
		t.Fatalf("expected 2 lap rows, got %d", len(got.PerLap))
	}
	for _, row := range got.PerLap {
		if len(row.Times) != 2 {
			t.Errorf("lap %d has %d sector times, want 2", row.Lap, len(row.Times))
		}
	}
	if got.Theoretical == nil {
		t.Error("expected a theoretical best when every sector was completed")
	}
	if len(got.BestFromLap) != 2 {
		t.Errorf("BestFromLap = %v", got.BestFromLap)
	}
}

func TestBuildJSONSectors_NoSectors(t *testing.T) {
	laps := []analysis.Lap{outputTestLap(2, 102.0)}
	if got := buildJSONSectors(laps, nil); got != nil {
		t.Errorf("expected nil with no sector boundaries, got %+v", got)
	}
}

func TestBuildJSONSectors_NoFlyingLaps(t *testing.T) {
	outLap := outputTestLap(1, 120)
	outLap.Kind = analysis.KindOutLap
	sectors := []analysis.Sector{{Num: 1, StartPct: 0.0}}

	if got := buildJSONSectors([]analysis.Lap{outLap}, sectors); got != nil {
		t.Errorf("expected nil with no flying laps, got %+v", got)
	}
}

// ---- corner conversion ----

// Wear is stored as the fraction remaining but reported as percent worn; the
// two are easy to confuse and inverted output would read as a fresh tyre.
func TestJSONCornerFrom_ConvertsWearToPercentWorn(t *testing.T) {
	got := jsonCornerFrom(analysis.CornerTyres{
		TempInner: 90, TempMid: 85, TempOuter: 80,
		WearInner: 0.90, WearMid: 0.95, WearOuter: 1.0,
		PressureKPa: 180,
	})

	if got.WornInner < 9.99 || got.WornInner > 10.01 {
		t.Errorf("WornInner = %v, want 10 (from 0.90 remaining)", got.WornInner)
	}
	if got.WornOuter != 0 {
		t.Errorf("WornOuter = %v, want 0 (from 1.0 remaining)", got.WornOuter)
	}
	if got.TempInnerC != 90 || got.PressureKPa != 180 {
		t.Errorf("passthrough fields wrong: %+v", got)
	}
}
