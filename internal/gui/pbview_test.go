package gui

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/pb"
)

// pbSetupYAML mirrors the shape iRacing writes and the analyze flow stores: a
// one-space-indented CarSetup block. Indentation and ordering both matter — the
// flattener walks it depth-first and the panel shows it in document order.
const pbSetupYAML = `CarSetup:
 UpdateCount: 5
 Tires:
  LeftFront:
   StartingPressure: 176 kPa
   LastHotPressure: 192 kPa
  RightFront:
   StartingPressure: 176 kPa
 Chassis:
  Front:
   ArbSetting: 3
`

func seedPB(t *testing.T, path string) {
	t.Helper()
	file := pb.File{
		// Deliberately inserted in an order that is neither the sorted one nor
		// a stable map order, so the sort under test has something to do.
		pb.Key("Porsche 718 GT4", "Watkins Glen"): {
			Car: "Porsche 718 GT4", Track: "Watkins Glen",
			LapTime: 114.787, LapTimeFormatted: "1:54.787",
			Date: "2026-06-21", Weather: "Track 31°C",
			Setup: pbSetupYAML,
			Phases: []pb.PBPhase{
				{SegName: "T1", Kind: "entry", SpeedEntryKPH: 196, SpeedExitKPH: 126, PeakBrakePct: 98},
			},
			BrakeEntries: pb.BrakeEntryMap{
				"T3": {Pct: 0.412, LapsUsed: 7},
				"T1": {Pct: 0.104, LapsUsed: 12},
			},
		},
		pb.Key("Porsche 718 GT4", "Donington Park"): {
			Car: "Porsche 718 GT4", Track: "Donington Park",
			LapTime: 70.067, LapTimeFormatted: "1:10.067",
			Date: "2026-04-07",
			// No setup, no phases — the "carries nothing but brake points"
			// case the list column has to render.
			BrakeEntries: pb.BrakeEntryMap{"T1": {Pct: 0.2, LapsUsed: 3}},
		},
		pb.Key("Ferrari 296", "Donington Park"): {
			Car: "Ferrari 296", Track: "Donington Park",
			LapTime: 69.1, LapTimeFormatted: "1:09.100",
		},
	}
	if err := pb.Save(path, file); err != nil {
		t.Fatalf("seeding pb.json: %v", err)
	}
}

func TestPBListSortsByTrackThenCar(t *testing.T) {
	s, _, dir := testServer(t, nil)
	seedPB(t, filepath.Join(dir, "pb.json"))

	got := decode[pbResponse](t, do(t, s, "GET", "/api/pb", ""))

	if len(got.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(got.Entries))
	}
	// Go map order is random, so an unsorted listing would reshuffle between
	// reloads — the same reason `pb list` sorts.
	want := [][2]string{
		{"Donington Park", "Ferrari 296"},
		{"Donington Park", "Porsche 718 GT4"},
		{"Watkins Glen", "Porsche 718 GT4"},
	}
	for i, w := range want {
		if got.Entries[i].Track != w[0] || got.Entries[i].Car != w[1] {
			t.Errorf("entry[%d] = %q / %q, want %q / %q",
				i, got.Entries[i].Track, got.Entries[i].Car, w[0], w[1])
		}
	}
}

// The list is drawn from a ~90 KB file, so it reports which payloads an entry
// carries without shipping them. Sending every setup to draw a five-column
// table would be most of that file for nothing.
func TestPBListOmitsHeavyPayloads(t *testing.T) {
	s, _, dir := testServer(t, nil)
	seedPB(t, filepath.Join(dir, "pb.json"))

	got := decode[pbResponse](t, do(t, s, "GET", "/api/pb", ""))

	var glen pbEntry
	for _, e := range got.Entries {
		if e.Track == "Watkins Glen" {
			glen = e
		}
	}

	if len(glen.Setup) != 0 || len(glen.Phases) != 0 || len(glen.BrakeEntries) != 0 {
		t.Error("the list view shipped the heavy payloads it exists to avoid")
	}
	// ... but it must still say they are there, or the table cannot show it.
	if !glen.HasSetup || !glen.HasPhases || glen.BrakePoint != 2 {
		t.Errorf("payload markers = setup %v, phases %v, brake points %d; want true, true, 2",
			glen.HasSetup, glen.HasPhases, glen.BrakePoint)
	}
}

func TestPBListMarksAnEntryCarryingNothing(t *testing.T) {
	s, _, dir := testServer(t, nil)
	seedPB(t, filepath.Join(dir, "pb.json"))

	got := decode[pbResponse](t, do(t, s, "GET", "/api/pb", ""))

	var ferrari pbEntry
	for _, e := range got.Entries {
		if e.Car == "Ferrari 296" {
			ferrari = e
		}
	}
	if ferrari.HasSetup || ferrari.HasPhases || ferrari.BrakePoint != 0 {
		t.Errorf("bare entry = %+v, want no payloads reported", ferrari)
	}
	if ferrari.LapTimeFormatted != "1:09.100" {
		t.Errorf("lap time = %q", ferrari.LapTimeFormatted)
	}
}

func TestPBDetailIncludesSetupPhasesAndBrakePoints(t *testing.T) {
	s, _, dir := testServer(t, nil)
	seedPB(t, filepath.Join(dir, "pb.json"))

	key := pb.Key("Porsche 718 GT4", "Watkins Glen")
	got := decode[pbResponse](t, do(t, s, "GET", "/api/pb?key="+urlQuery(key), ""))

	if len(got.Entries) != 1 {
		t.Fatalf("got %d entries for one key, want 1", len(got.Entries))
	}
	e := got.Entries[0]

	if len(e.Phases) != 1 || e.Phases[0].SegName != "T1" {
		t.Errorf("phases = %+v", e.Phases)
	}
	if len(e.Setup) == 0 {
		t.Fatal("detail view carries no setup")
	}
	// Brake points are sorted by segment so the table does not reshuffle.
	if len(e.BrakeEntries) != 2 || e.BrakeEntries[0].Segment != "T1" || e.BrakeEntries[1].Segment != "T3" {
		t.Errorf("brake entries = %+v, want T1 then T3", e.BrakeEntries)
	}
	if e.BrakeEntries[0].LapsUsed != 12 {
		t.Errorf("lapsUsed = %d, want 12 — the weighting behind the average is dropped",
			e.BrakeEntries[0].LapsUsed)
	}
}

// FlattenSetup preserves document order, which is the order the garage screen
// uses — the order someone comparing the page against the sim reads in. Sorting
// the paths would scramble that for no gain.
func TestPBDetailKeepsSetupInDocumentOrder(t *testing.T) {
	s, _, dir := testServer(t, nil)
	seedPB(t, filepath.Join(dir, "pb.json"))

	key := pb.Key("Porsche 718 GT4", "Watkins Glen")
	got := decode[pbResponse](t, do(t, s, "GET", "/api/pb?key="+urlQuery(key), ""))

	paths := make([]string, 0, len(got.Entries[0].Setup))
	for _, f := range got.Entries[0].Setup {
		paths = append(paths, f.Path)
	}
	joined := strings.Join(paths, " ")

	if paths[0] != "UpdateCount" {
		t.Errorf("first setup field = %q, want UpdateCount (document order)", paths[0])
	}
	// Alphabetical would put Chassis/... ahead of Tires/...; document order does
	// not.
	tires := indexOfPrefix(paths, "Tires/")
	chassis := indexOfPrefix(paths, "Chassis/")
	if tires < 0 || chassis < 0 {
		t.Fatalf("expected both sections in %v", paths)
	}
	if tires > chassis {
		t.Errorf("setup was sorted rather than left in document order: %s", joined)
	}

	// Nested leaves keep their full path, so a value is identifiable.
	if !strings.Contains(joined, "Tires/LeftFront/StartingPressure") {
		t.Errorf("nested path missing from %s", joined)
	}
}

func TestPBDetailUnknownKeyIs404(t *testing.T) {
	s, _, dir := testServer(t, nil)
	seedPB(t, filepath.Join(dir, "pb.json"))

	w := do(t, s, "GET", "/api/pb?key=Nothing%7CNowhere", "")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(decode[errorBody](t, w).Error, "Nothing") {
		t.Errorf("error = %q, want the key named back", w.Body.String())
	}
}

// A rig that has never set a PB has no pb.json, which is the normal starting
// state and not an error.
func TestPBListWithNoStoreIsEmptyNotAnError(t *testing.T) {
	s, _, _ := testServer(t, nil) // no pb.json seeded

	w := do(t, s, "GET", "/api/pb", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := decode[pbResponse](t, w); len(got.Entries) != 0 {
		t.Errorf("entries = %+v, want none", got.Entries)
	}
}

func TestPBListReportsAnUnreadableStore(t *testing.T) {
	s, _, dir := testServer(t, nil)
	path := filepath.Join(dir, "pb.json")
	if err := writeFile(path, "{ not json"); err != nil {
		t.Fatal(err)
	}

	w := do(t, s, "GET", "/api/pb", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(decode[errorBody](t, w).Error, "pb.json") {
		t.Errorf("error = %q, want it to name the file", w.Body.String())
	}
}

/* ── helpers ───────────────────────────────────────────────────────── */

func indexOfPrefix(paths []string, prefix string) int {
	for i, p := range paths {
		if strings.HasPrefix(p, prefix) {
			return i
		}
	}
	return -1
}
