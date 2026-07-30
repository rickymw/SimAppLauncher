package main

import (
	"testing"

	"github.com/rickymw/MotorHome/internal/pb"
)

func testPBFile() pb.File {
	return pb.File{
		pb.Key("Porsche 718 GT4", "Watkins Glen"): {
			Car: "Porsche 718 GT4", Track: "Watkins Glen",
			LapTime: 130.5, LapTimeFormatted: "2:10.500", Date: "2026-07-01",
		},
		pb.Key("Porsche 718 GT4", "Road America"): {
			Car: "Porsche 718 GT4", Track: "Road America",
			LapTime: 145.2, LapTimeFormatted: "2:25.200", Date: "2026-01-15",
		},
		pb.Key("BMW M4 GT3", "Watkins Glen"): {
			Car: "BMW M4 GT3", Track: "Watkins Glen",
			LapTime: 120.1, LapTimeFormatted: "2:00.100", Date: "2026-07-20",
		},
	}
}

// Map iteration order is random, so listings and prune previews must impose
// their own order or they change between runs.
func TestPBEntries_SortedByTrackThenCar(t *testing.T) {
	got := pbEntries(testPBFile())
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	want := [][2]string{
		{"Porsche 718 GT4", "Road America"},
		{"BMW M4 GT3", "Watkins Glen"},
		{"Porsche 718 GT4", "Watkins Glen"},
	}
	for i, w := range want {
		if got[i].Car != w[0] || got[i].Track != w[1] {
			t.Errorf("entry %d = %s | %s, want %s | %s", i, got[i].Car, got[i].Track, w[0], w[1])
		}
	}
}

func TestMatchPBEntries(t *testing.T) {
	entries := pbEntries(testPBFile())

	tests := []struct {
		filter string
		want   int
	}{
		{"", 3},                  // empty matches everything
		{"watkins", 2},           // case-insensitive track match
		{"WATKINS GLEN", 2},      // fully uppercase
		{"porsche", 2},           // car match
		{"bmw", 1},               // single match
		{"718 GT4 | Watkins", 1}, // matches across the "car | track" join
		{"nonexistent", 0},
	}
	for _, tt := range tests {
		if got := len(matchPBEntries(entries, tt.filter)); got != tt.want {
			t.Errorf("matchPBEntries(%q) matched %d, want %d", tt.filter, got, tt.want)
		}
	}
}

func TestStoredMarkers(t *testing.T) {
	tests := []struct {
		name  string
		entry *pb.PersonalBest
		want  string
	}{
		{
			name:  "nothing stored",
			entry: &pb.PersonalBest{},
			want:  "—",
		},
		{
			name: "phases and setup",
			entry: &pb.PersonalBest{
				Phases: []pb.PBPhase{{SegName: "T1"}, {SegName: "T2"}},
				Setup:  "CarSetup:\n",
			},
			want: "2 phases, setup",
		},
		{
			name: "brake entries only",
			entry: &pb.PersonalBest{
				BrakeEntries: pb.BrakeEntryMap{"T1": {Pct: 0.1}},
			},
			want: "1 brake pts",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := storedMarkers(tt.entry); got != tt.want {
				t.Errorf("storedMarkers = %q, want %q", got, tt.want)
			}
		})
	}
}
