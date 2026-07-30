package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
)

func TestPhasesToPB(t *testing.T) {
	lap := outputTestLap(4, 101.5)
	phases := analysis.ComputePhases(&lap, outputTestSegs(), nil)
	if len(phases) == 0 {
		t.Fatal("fixture produced no phases")
	}

	got := phasesToPB(phases)
	if len(got) != len(phases) {
		t.Fatalf("phasesToPB returned %d, want %d", len(got), len(phases))
	}
	for i := range phases {
		if got[i].SegName != phases[i].SegName {
			t.Errorf("phase %d SegName = %q, want %q", i, got[i].SegName, phases[i].SegName)
		}
		// Kind crosses from a typed PhaseKind to a plain string in storage;
		// a silent mismatch here would break every future vs-PB lookup.
		if got[i].Kind != string(phases[i].Kind) {
			t.Errorf("phase %d Kind = %q, want %q", i, got[i].Kind, phases[i].Kind)
		}
		if got[i].SpeedEntryKPH != phases[i].SpeedEntryKPH ||
			got[i].CoastSamples != phases[i].CoastSamples ||
			got[i].SampleCount != phases[i].SampleCount {
			t.Errorf("phase %d values not carried over: %+v vs %+v", i, got[i], phases[i])
		}
	}
}

// The stored kinds must be exactly what PhaseKey expects, or the vs-PB table
// silently matches nothing.
func TestPhasesToPB_RoundTripsThroughPhaseLookup(t *testing.T) {
	lap := outputTestLap(4, 101.5)
	phases := analysis.ComputePhases(&lap, outputTestSegs(), nil)

	lookup := pb.PhaseLookup(phasesToPB(phases))
	for _, p := range phases {
		if _, ok := lookup[pb.PhaseKey(p.SegName, string(p.Kind))]; !ok {
			t.Errorf("phase %s/%s did not round-trip through PhaseLookup", p.SegName, p.Kind)
		}
	}
}

func storedPBFixture() *pb.PersonalBest {
	return &pb.PersonalBest{
		Car:              "Porsche 718 GT4",
		Track:            "Watkins Glen",
		LapTime:          114.787,
		LapTimeFormatted: "1:54.787",
		Date:             "2026-06-21",
		Weather:          "Track 35°C",
		Setup:            testSetupYAML,
		Phases: []pb.PBPhase{
			{SegName: "T1", Kind: "entry", SampleCount: 120, SpeedEntryKPH: 200, SpeedExitKPH: 150},
			{SegName: "T1", Kind: "exit", SampleCount: 90, SpeedEntryKPH: 150, SpeedExitKPH: 180},
		},
	}
}

func TestPrintStoredPB(t *testing.T) {
	out := captureAnalyzeOut(t, func() { printStoredPB(storedPBFixture()) })

	for _, want := range []string{
		"Porsche 718 GT4", "Watkins Glen", "1:54.787", "2026-06-21", "Track 35°C",
		"Tyres:", "Suspension:", // setup tables rendered from the stored YAML
		"PB lap", "T1", "entry", "exit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stored PB output missing %q:\n%s", want, out)
		}
	}
}

// An entry written before phase capture existed still renders its header; it
// must say why the table is missing rather than printing an empty one.
func TestPrintStoredPB_NoPhases(t *testing.T) {
	entry := storedPBFixture()
	entry.Phases = nil

	out := captureAnalyzeOut(t, func() { printStoredPB(entry) })
	if !strings.Contains(out, "no phase data stored") {
		t.Errorf("expected an explanation for the missing phase table:\n%s", out)
	}
}

func TestPrintStoredPB_NoSetup(t *testing.T) {
	entry := storedPBFixture()
	entry.Setup = ""

	out := captureAnalyzeOut(t, func() { printStoredPB(entry) })
	if strings.Contains(out, "Suspension:") {
		t.Errorf("no setup stored, so no setup tables should render:\n%s", out)
	}
	if !strings.Contains(out, "1:54.787") {
		t.Errorf("header should still render:\n%s", out)
	}
}

// Missing car/track/date must render as placeholders rather than blanks that
// silently shift the layout.
func TestPrintStoredPB_UnknownFields(t *testing.T) {
	entry := &pb.PersonalBest{LapTimeFormatted: "1:00.000"}

	out := captureAnalyzeOut(t, func() { printStoredPB(entry) })
	if !strings.Contains(out, "(unknown)") || !strings.Contains(out, "weather unknown") {
		t.Errorf("expected placeholders for absent fields:\n%s", out)
	}
}

func TestPrintStoredPhaseTable_SkipsEmptyPhases(t *testing.T) {
	phases := []pb.PBPhase{
		{SegName: "T1", Kind: "full", SampleCount: 10},
		{SegName: "GHOST", Kind: "full", SampleCount: 0},
	}
	out := captureAnalyzeOut(t, func() { printStoredPhaseTable("1:00.000", phases) })
	if strings.Contains(out, "GHOST") {
		t.Errorf("zero-sample stored phase was rendered:\n%s", out)
	}
}

func TestWriteStoredPBJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := writeStoredPBJSON(&buf, storedPBFixture()); err != nil {
		t.Fatalf("writeStoredPBJSON: %v", err)
	}

	var back pb.PersonalBest
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if back.LapTimeFormatted != "1:54.787" || back.Car != "Porsche 718 GT4" {
		t.Errorf("record did not round-trip: %+v", back)
	}
	if len(back.Phases) != 2 {
		t.Errorf("phases did not round-trip: %+v", back.Phases)
	}
}

// -lap pb -json must emit the record only, with no table output leaking onto
// stdout alongside the document.
func TestEmitStoredPB_JSONModeEmitsNoTables(t *testing.T) {
	out := captureAnalyzeOut(t, func() {
		stdout := captureStdoutForTest(t, func() {
			emitStoredPB(storedPBFixture(), true)
		})
		if !strings.HasPrefix(strings.TrimSpace(stdout), "{") {
			t.Errorf("expected a JSON document on stdout, got:\n%s", stdout)
		}
	})
	if out != "" {
		t.Errorf("no tables should be written to the analyze sink in JSON mode:\n%s", out)
	}
}

func TestEmitStoredPB_TableMode(t *testing.T) {
	out := captureAnalyzeOut(t, func() { emitStoredPB(storedPBFixture(), false) })
	if !strings.Contains(out, "PB lap") {
		t.Errorf("expected the phase table in non-JSON mode:\n%s", out)
	}
}

// captureStdoutForTest reuses the clipboard capture helper to grab os.Stdout
// for the duration of fn.
func captureStdoutForTest(t *testing.T, fn func()) string {
	t.Helper()
	finish, err := captureStdout()
	if err != nil {
		t.Fatalf("captureStdout: %v", err)
	}
	fn()
	return finish()
}
