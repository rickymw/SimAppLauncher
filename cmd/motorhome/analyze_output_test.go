package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// ---- shared fixtures ----

// outputTestSegs is a two-segment map: one straight, one corner.
func outputTestSegs() []trackmap.Segment {
	return []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}
}

// outputTestLap builds a lap that spans both segments, steers through the
// corner half, and carries enough channels for the tyre and phase tables.
func outputTestLap(number int, lapTime float32) analysis.Lap {
	var samples []analysis.SampleData
	const n = 600
	for i := 0; i < n; i++ {
		pct := float32(i) / float32(n)
		s := analysis.SampleData{
			LapDistPct:  pct,
			SessionTime: float64(number*1000) + float64(i)/60.0,
			Speed:       50,
			Throttle:    1.0,
			Gear:        4,
			LatAccel:    2.0,
			LongAccel:   1.0,
			BrakeBias:   51.5,
			LFtempL:     80, LFtempM: 82, LFtempR: 84,
			RFtempL: 81, RFtempM: 83, RFtempR: 85,
			LRtempL: 78, LRtempM: 79, LRtempR: 80,
			RRtempL: 77, RRtempM: 78, RRtempR: 79,
			LFwearL: 0.95, LFwearM: 0.96, LFwearR: 0.97,
			RFwearL: 0.95, RFwearM: 0.96, RFwearR: 0.97,
			LRwearL: 0.98, LRwearM: 0.98, LRwearR: 0.99,
			RRwearL: 0.98, RRwearM: 0.98, RRwearR: 0.99,
			LFpressure: 180, RFpressure: 181, LRpressure: 179, RRpressure: 178,
		}
		if pct >= 0.5 {
			// Ramp steering up, hold, then unwind. A constant angle would sit
			// at 100% of peak for every sample and collapse the corner into a
			// single "mid" phase, which is not what a real corner looks like.
			u := (pct - 0.5) / 0.5 // 0→1 across the corner
			var frac float32
			switch {
			case u < 0.33:
				frac = u / 0.33
			case u > 0.67:
				frac = (1 - u) / 0.33
			default:
				frac = 1
			}
			s.SteeringAngle = 0.5 * frac
			s.Throttle = 0.2
			s.Brake = 0.6
		}
		samples = append(samples, s)
	}
	return analysis.Lap{
		Number:  number,
		LapTime: lapTime,
		Kind:    analysis.KindFlying,
		Samples: samples,
	}
}

// ---- phase table ----

func TestPrintPhaseTable(t *testing.T) {
	lap := outputTestLap(4, 101.5)
	phases := analysis.ComputePhases(&lap, outputTestSegs(), nil)

	out := captureAnalyzeOut(t, func() { printPhaseTable(&lap, phases) })

	for _, want := range []string{"Lap 4", "1:41.500", "Name", "Phase", "S1", "T1", "full", "entry"} {
		if !strings.Contains(out, want) {
			t.Errorf("phase table missing %q:\n%s", want, out)
		}
	}
}

// A phase with no samples carries no information and must not produce a row.
func TestPrintPhaseTable_SkipsEmptyPhases(t *testing.T) {
	lap := outputTestLap(1, 60)
	phases := []analysis.Phase{
		{SegName: "T1", Kind: analysis.PhaseFull, SampleCount: 10, SpeedEntryKPH: 100},
		{SegName: "GHOST", Kind: analysis.PhaseFull, SampleCount: 0},
	}

	out := captureAnalyzeOut(t, func() { printPhaseTable(&lap, phases) })
	if strings.Contains(out, "GHOST") {
		t.Errorf("zero-sample phase was rendered:\n%s", out)
	}
}

// The name column has to widen to the longest segment name, or long labels
// from trackref.json cornerNames would break the alignment.
func TestPrintPhaseTable_WidensForLongSegmentNames(t *testing.T) {
	lap := outputTestLap(1, 60)
	phases := []analysis.Phase{
		{SegName: "T11 Kink at the Carousel", Kind: analysis.PhaseFull, SampleCount: 10},
	}

	out := captureAnalyzeOut(t, func() { printPhaseTable(&lap, phases) })
	if !strings.Contains(out, "T11 Kink at the Carousel |") {
		t.Errorf("long segment name not padded into its own column:\n%s", out)
	}
}

// ---- vs PB comparison ----

func TestPrintPBComparison(t *testing.T) {
	current := []analysis.Phase{
		{SegName: "T1", Kind: analysis.PhaseEntry, SampleCount: 100,
			SpeedEntryKPH: 200, SpeedExitKPH: 150, CoastSamples: 60},
	}
	stored := []pb.PBPhase{
		{SegName: "T1", Kind: "entry", SampleCount: 100,
			SpeedEntryKPH: 190, SpeedExitKPH: 155, CoastSamples: 120},
	}

	out := captureAnalyzeOut(t, func() { printPBComparison(current, stored) })

	if !strings.Contains(out, "vs PB") {
		t.Fatalf("missing vs PB header:\n%s", out)
	}
	// Faster entry (+10), slower exit (−5), one second less coast (−1.00s).
	for _, want := range []string{"+10", "-5", "-1.00s"} {
		if !strings.Contains(out, want) {
			t.Errorf("delta %q not rendered:\n%s", want, out)
		}
	}
}

// With no overlapping segment/phase pairs there is nothing to compare, and a
// header over an empty table would imply otherwise.
func TestPrintPBComparison_NoMatchingPhasesPrintsNothing(t *testing.T) {
	current := []analysis.Phase{{SegName: "T1", Kind: analysis.PhaseEntry, SampleCount: 10}}
	stored := []pb.PBPhase{{SegName: "T9", Kind: "exit", SampleCount: 10}}

	if out := captureAnalyzeOut(t, func() { printPBComparison(current, stored) }); out != "" {
		t.Errorf("expected no output when no phases match, got:\n%s", out)
	}
}

// A phase present now but absent from the PB is skipped rather than compared
// against a zero value, which would render as a huge bogus delta.
func TestPrintPBComparison_SkipsUnmatchedRows(t *testing.T) {
	current := []analysis.Phase{
		{SegName: "T1", Kind: analysis.PhaseEntry, SampleCount: 10, SpeedEntryKPH: 100},
		{SegName: "NEWSEG", Kind: analysis.PhaseFull, SampleCount: 10, SpeedEntryKPH: 100},
	}
	stored := []pb.PBPhase{{SegName: "T1", Kind: "entry", SampleCount: 10, SpeedEntryKPH: 100}}

	out := captureAnalyzeOut(t, func() { printPBComparison(current, stored) })
	if strings.Contains(out, "NEWSEG") {
		t.Errorf("unmatched phase should be skipped, not compared to zero:\n%s", out)
	}
}

// ---- exit impact ----

func TestPrintExitImpact(t *testing.T) {
	impacts := []analysis.ExitImpact{
		{CornerName: "T1", CornerExitSpeedKPH: 172.8, StraightName: "S2", StraightPeakSpeedKPH: 185.3},
	}
	out := captureAnalyzeOut(t, func() { printExitImpact(impacts) })

	for _, want := range []string{"Corner Exit", "T1", "172.8", "S2", "185.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("exit impact missing %q:\n%s", want, out)
		}
	}
}

func TestPrintExitImpact_EmptyPrintsNothing(t *testing.T) {
	if out := captureAnalyzeOut(t, func() { printExitImpact(nil) }); out != "" {
		t.Errorf("expected no output for zero impacts, got %q", out)
	}
}

// ---- tyres ----

func TestPrintTyreSummary(t *testing.T) {
	lap := outputTestLap(4, 101.5)
	out := captureAnalyzeOut(t, func() { printTyreSummary(&lap) })

	for _, want := range []string{"Tyres (Lap 4", "LF", "RF", "LR", "RR", "Press"} {
		if !strings.Contains(out, want) {
			t.Errorf("tyre summary missing %q:\n%s", want, out)
		}
	}
}

// Some .ibt files have no tyre temp channels at all; a table of zeroes would
// look like ice-cold tyres rather than absent data.
func TestPrintTyreSummary_NoTempChannelsPrintsNothing(t *testing.T) {
	lap := analysis.Lap{
		Number:  1,
		Samples: []analysis.SampleData{{LapDistPct: 0.1, Speed: 50}},
	}
	if out := captureAnalyzeOut(t, func() { printTyreSummary(&lap) }); out != "" {
		t.Errorf("expected no tyre table when temps are absent, got:\n%s", out)
	}
}

// ---- zones (no-track-map fallback) ----

func TestPrintZoneTable(t *testing.T) {
	lap := outputTestLap(2, 95)
	out := captureAnalyzeOut(t, func() { printZoneTable(&lap, analysis.ZoneStats(&lap)) })

	for _, want := range []string{"Lap 2", "Zone", "EntSpd", "MinSpd"} {
		if !strings.Contains(out, want) {
			t.Errorf("zone table missing %q:\n%s", want, out)
		}
	}
}

// A zone with no samples renders as dashes rather than as zeroes.
func TestPrintZoneTable_EmptyZoneRendersDashes(t *testing.T) {
	lap := outputTestLap(1, 60)
	zones := []analysis.Zone{{Index: 0, SampleCount: 0}}

	out := captureAnalyzeOut(t, func() { printZoneTable(&lap, zones) })
	if !strings.Contains(out, "---") {
		t.Errorf("expected dashes for an empty zone:\n%s", out)
	}
}

// ---- sectors ----

func sectorTestLaps() []analysis.Lap {
	return []analysis.Lap{outputTestLap(2, 102.0), outputTestLap(3, 101.5)}
}

func TestPrintSectorTable(t *testing.T) {
	sectors := []analysis.Sector{{Num: 1, StartPct: 0.0}, {Num: 2, StartPct: 0.5}}

	out := captureAnalyzeOut(t, func() { printSectorTable(sectorTestLaps(), sectors) })

	for _, want := range []string{"Sectors:", "S1", "S2", "best"} {
		if !strings.Contains(out, want) {
			t.Errorf("sector table missing %q:\n%s", want, out)
		}
	}
	// The fastest time in each sector is marked.
	if !strings.Contains(out, "*") {
		t.Errorf("expected a best-sector marker:\n%s", out)
	}
}

// Some session types publish no SplitTimeInfo block at all.
func TestPrintSectorTable_NoSectorsPrintsNothing(t *testing.T) {
	if out := captureAnalyzeOut(t, func() { printSectorTable(sectorTestLaps(), nil) }); out != "" {
		t.Errorf("expected no output with no sectors, got:\n%s", out)
	}
}

// Out laps and in laps have no meaningful sector times and must not appear.
func TestPrintSectorTable_SkipsNonFlyingLaps(t *testing.T) {
	outLap := outputTestLap(1, 120)
	outLap.Kind = analysis.KindOutLap
	sectors := []analysis.Sector{{Num: 1, StartPct: 0.0}, {Num: 2, StartPct: 0.5}}

	out := captureAnalyzeOut(t, func() {
		printSectorTable([]analysis.Lap{outLap}, sectors)
	})
	if out != "" {
		t.Errorf("out lap should not produce a sector table:\n%s", out)
	}
}

// ---- setup tables ----

const testSetupYAML = `CarSetup:
 UpdateCount: 5
 Tires:
  LeftFront:
   StartingPressure: 176 kPa
   LastTempsOMI: 82C, 86C, 91C
  RightFront:
   StartingPressure: 176 kPa
   LastTempsIMO: 80C, 73C, 67C
 Chassis:
  FrontBrakes:
   ArbSetting: 2
   BrakePads: Endurance
  LeftFront:
   Camber: -4.0 deg
  RightFront:
   Camber: -4.0 deg
`

func TestPrintSetupTables(t *testing.T) {
	nodes := analysis.ParseCarSetupTree(testSetupYAML)
	if nodes == nil {
		t.Fatal("fixture YAML did not parse")
	}

	out := captureAnalyzeOut(t, func() { printSetupTables(nodes) })

	for _, want := range []string{"Tyres:", "Suspension:", "LF", "RF", "StartingPressure", "Camber"} {
		if !strings.Contains(out, want) {
			t.Errorf("setup tables missing %q:\n%s", want, out)
		}
	}
	// iRacing names the same reading differently per side; the two are merged
	// into one row so the corners line up.
	if strings.Contains(out, "LastTempsOMI") || strings.Contains(out, "LastTempsIMO") {
		t.Errorf("per-side temp keys should be merged into LastTemps:\n%s", out)
	}
	if !strings.Contains(out, "LastTemps") {
		t.Errorf("merged LastTemps row missing:\n%s", out)
	}
	// Non-corner entries are printed as plain key/value lines.
	if !strings.Contains(out, "ArbSetting: 2") {
		t.Errorf("general (non-corner) setting missing:\n%s", out)
	}
}

func TestPrintCornerTable_GeneralOnly(t *testing.T) {
	children := []analysis.SetupNode{{Key: "TireType", Value: "Dry"}}
	out := captureAnalyzeOut(t, func() { printCornerTable("Tyres", children) })

	if !strings.Contains(out, "TireType: Dry") {
		t.Errorf("expected general entry to render:\n%s", out)
	}
}

// ---- dump ----

func TestRunDump_SingleLap(t *testing.T) {
	dir := t.TempDir()
	lap := outputTestLap(4, 101.5)
	opts := singleLapOpts{
		segs:    outputTestSegs(),
		dumpSeg: "T1",
		dumpDir: dir,
	}

	out := captureAnalyzeOut(t, func() { runDump(opts, &lap) })

	csvPath := filepath.Join(dir, "T1_lap4.csv")
	if _, err := os.Stat(csvPath); err != nil {
		t.Fatalf("expected CSV at %s: %v", csvPath, err)
	}
	if !strings.Contains(out, "Dumped T1") {
		t.Errorf("expected a confirmation line:\n%s", out)
	}

	body, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Dist%,Time,Speed") {
		t.Errorf("single-lap CSV has no header row:\n%s", body)
	}
}

func TestRunDump_AllLaps(t *testing.T) {
	dir := t.TempDir()
	lap3 := outputTestLap(3, 102.0)
	lap4 := outputTestLap(4, 101.5)
	opts := singleLapOpts{
		segs:           outputTestSegs(),
		comparableLaps: []analysis.Lap{lap3, lap4},
		dumpSeg:        "T1",
		dumpDir:        dir,
		dumpAllLaps:    true,
	}

	out := captureAnalyzeOut(t, func() { runDump(opts, &lap4) })

	csvPath := filepath.Join(dir, "T1_alllaps.csv")
	body, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("expected CSV at %s: %v", csvPath, err)
	}
	if !strings.Contains(string(body), "Lap,Dist%") {
		t.Errorf("all-laps CSV missing the Lap column:\n%s", body)
	}
	if !strings.Contains(out, "for 2 laps") {
		t.Errorf("expected a two-lap confirmation:\n%s", out)
	}
}

// A segment index resolves the same as a segment name.
func TestRunDump_ResolvesSegmentByIndex(t *testing.T) {
	dir := t.TempDir()
	lap := outputTestLap(4, 101.5)
	opts := singleLapOpts{segs: outputTestSegs(), dumpSeg: "2", dumpDir: dir}

	captureAnalyzeOut(t, func() { runDump(opts, &lap) })

	if _, err := os.Stat(filepath.Join(dir, "T1_lap4.csv")); err != nil {
		t.Errorf("1-based index 2 should resolve to segment T1: %v", err)
	}
}
