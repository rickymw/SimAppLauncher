package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/pb"
)

// writeTestPBFile writes a pb.json into a temp dir and returns its path.
// Dates are relative to now so the -older-than tests don't rot.
func writeTestPBFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pb.json")

	recent := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	old := time.Now().AddDate(0, 0, -400).Format("2006-01-02")

	pbf := pb.File{
		pb.Key("Porsche 718 GT4", "Watkins Glen"): {
			Car: "Porsche 718 GT4", Track: "Watkins Glen",
			LapTime: 114.787, LapTimeFormatted: "1:54.787", Date: recent,
			Setup:  testSetupYAML,
			Phases: []pb.PBPhase{{SegName: "T1", Kind: "full", SampleCount: 10}},
		},
		pb.Key("Porsche 718 GT4", "Sebring"): {
			Car: "Porsche 718 GT4", Track: "Sebring",
			LapTime: 131.367, LapTimeFormatted: "2:11.367", Date: old,
		},
		pb.Key("BMW M4 GT3", "Watkins Glen"): {
			Car: "BMW M4 GT3", Track: "Watkins Glen",
			LapTime: 120.1, LapTimeFormatted: "2:00.100", Date: old,
			BrakeEntries: pb.BrakeEntryMap{"T1": {Pct: 0.12, LapsUsed: 4}},
		},
	}
	if err := pb.Save(path, pbf); err != nil {
		t.Fatalf("seeding pb.json: %v", err)
	}
	return path
}

func TestRunPBList(t *testing.T) {
	path := writeTestPBFile(t)

	out := captureStdoutForTest(t, func() { runPBList(nil, path) })

	for _, want := range []string{
		"Car", "Track", "Porsche 718 GT4", "BMW M4 GT3",
		"Watkins Glen", "Sebring", "1:54.787", "3 entries",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pb list missing %q:\n%s", want, out)
		}
	}
	// The stored-payload column tells you which entries can produce a vs-PB
	// table or be diffed.
	if !strings.Contains(out, "1 phases, setup") {
		t.Errorf("expected stored-payload markers:\n%s", out)
	}
}

// Sorting is by track then car, so listings don't shuffle between runs.
func TestRunPBList_StableOrder(t *testing.T) {
	path := writeTestPBFile(t)

	first := captureStdoutForTest(t, func() { runPBList(nil, path) })
	second := captureStdoutForTest(t, func() { runPBList(nil, path) })

	if first != second {
		t.Errorf("pb list output is not stable between runs:\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
	if strings.Index(first, "Sebring") > strings.Index(first, "Watkins") {
		t.Errorf("entries should be sorted by track:\n%s", first)
	}
}

func TestRunPBList_Filtered(t *testing.T) {
	path := writeTestPBFile(t)

	out := captureStdoutForTest(t, func() { runPBList([]string{"bmw"}, path) })

	if !strings.Contains(out, "BMW M4 GT3") {
		t.Errorf("filter should match the BMW entry:\n%s", out)
	}
	if strings.Contains(out, "Porsche") {
		t.Errorf("filter should exclude non-matching entries:\n%s", out)
	}
	if !strings.Contains(out, "1 entry") {
		t.Errorf("expected a singular count:\n%s", out)
	}
}

func TestRunPBShow(t *testing.T) {
	path := writeTestPBFile(t)

	out := captureStdoutForTest(t, func() { runPBShow([]string{"sebring"}, path) })

	if !strings.Contains(out, "Sebring") || !strings.Contains(out, "2:11.367") {
		t.Errorf("pb show did not render the matched entry:\n%s", out)
	}
}

// ---- prune ----

// The destructive path must be opt-in: a bare prune previews and writes nothing.
func TestRunPBPrune_DryRunDoesNotWrite(t *testing.T) {
	path := writeTestPBFile(t)

	out := captureStdoutForTest(t, func() {
		runPBPrune([]string{"-older-than", "90"}, path)
	})

	if !strings.Contains(out, "Would remove 2") {
		t.Errorf("expected a preview of 2 entries:\n%s", out)
	}
	if !strings.Contains(out, "Dry run") {
		t.Errorf("expected an explicit dry-run notice:\n%s", out)
	}

	after, err := pb.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 3 {
		t.Errorf("dry run modified the store: %d entries remain, want 3", len(after))
	}
}

func TestRunPBPrune_ApplyRemovesEntries(t *testing.T) {
	path := writeTestPBFile(t)

	out := captureStdoutForTest(t, func() {
		runPBPrune([]string{"-older-than", "90", "-apply"}, path)
	})

	if !strings.Contains(out, "Removed 2") {
		t.Errorf("expected a removal confirmation:\n%s", out)
	}

	after, err := pb.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Fatalf("expected 1 entry to survive, got %d", len(after))
	}
	// The recent entry is the one that should remain.
	if _, ok := after[pb.Key("Porsche 718 GT4", "Watkins Glen")]; !ok {
		t.Errorf("the recent entry was removed; store now: %v", after)
	}
}

func TestRunPBPrune_MatchFilter(t *testing.T) {
	path := writeTestPBFile(t)

	out := captureStdoutForTest(t, func() {
		runPBPrune([]string{"-match", "bmw", "-apply"}, path)
	})
	if !strings.Contains(out, "Removed 1") {
		t.Errorf("expected exactly one removal:\n%s", out)
	}

	after, _ := pb.Load(path)
	if _, ok := after[pb.Key("BMW M4 GT3", "Watkins Glen")]; ok {
		t.Error("the matched entry was not removed")
	}
	if len(after) != 2 {
		t.Errorf("expected 2 survivors, got %d", len(after))
	}
}

func TestRunPBPrune_NothingMatches(t *testing.T) {
	path := writeTestPBFile(t)

	out := captureStdoutForTest(t, func() {
		runPBPrune([]string{"-match", "ferrari"}, path)
	})
	if !strings.Contains(out, "Nothing to prune") {
		t.Errorf("expected a no-match message:\n%s", out)
	}

	after, _ := pb.Load(path)
	if len(after) != 3 {
		t.Errorf("store should be untouched, got %d entries", len(after))
	}
}

// An entry whose date will not parse cannot be shown to be stale, so it is
// kept — but silently skipping it would make the prune look complete.
func TestRunPBPrune_ReportsUnparseableDates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pb.json")
	pbf := pb.File{
		pb.Key("Car A", "Track A"): {Car: "Car A", Track: "Track A", Date: "not-a-date"},
	}
	if err := pb.Save(path, pbf); err != nil {
		t.Fatal(err)
	}

	out := captureStdoutForTest(t, func() {
		runPBPrune([]string{"-older-than", "1", "-apply"}, path)
	})
	if !strings.Contains(out, "Nothing to prune") {
		t.Errorf("an undated entry must not be selected for removal:\n%s", out)
	}

	after, _ := pb.Load(path)
	if len(after) != 1 {
		t.Errorf("undated entry was removed; %d remain", len(after))
	}
}

// -older-than counts from the PB date, so a recent entry survives a large
// threshold and nothing is removed by accident.
func TestRunPBPrune_RecentEntriesSurvive(t *testing.T) {
	path := writeTestPBFile(t)

	captureStdoutForTest(t, func() {
		runPBPrune([]string{"-older-than", "5000", "-apply"}, path)
	})

	after, _ := pb.Load(path)
	if len(after) != 3 {
		t.Errorf("no entry is 5000 days old; %d were removed", 3-len(after))
	}
}

// ---- dispatch ----

func TestRunPB_DefaultsToList(t *testing.T) {
	path := writeTestPBFile(t)

	bare := captureStdoutForTest(t, func() { RunPB(nil, testConfig(), path) })
	explicit := captureStdoutForTest(t, func() { RunPB([]string{"list"}, testConfig(), path) })

	if bare != explicit {
		t.Errorf("bare `pb` should behave as `pb list`:\n--- bare ---\n%s\n--- list ---\n%s",
			bare, explicit)
	}
	if !strings.Contains(bare, "entries in") {
		t.Errorf("expected a listing:\n%s", bare)
	}
}

// A filter needs the explicit `list` subcommand: a bare `pb bmw` is read as an
// unknown subcommand, because a car name cannot be distinguished from a
// mistyped subcommand without guessing.
func TestRunPB_FilterRequiresExplicitList(t *testing.T) {
	path := writeTestPBFile(t)

	out := captureStdoutForTest(t, func() { RunPB([]string{"list", "bmw"}, testConfig(), path) })
	if !strings.Contains(out, "BMW M4 GT3") {
		t.Errorf("expected a filtered listing:\n%s", out)
	}
	if strings.Contains(out, "Porsche") {
		t.Errorf("filter should have excluded the Porsche entries:\n%s", out)
	}
}

// testConfig returns a config with no ibtDir, so pb subcommands that would
// otherwise scan for telemetry stay hermetic.
func testConfig() config.Config { return config.Config{} }
