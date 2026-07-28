package main

import (
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// ---- formatMapLine ----
//
// These cover the regression where the map line branched on matchScore rather
// than on whether a stored map was used. Extracting formatMapLine out of the
// 1100-line RunAnalyze is what made them possible to write at all.

func TestFormatMapLine_ExistingMapWithMatchScore(t *testing.T) {
	tm := &trackmap.TrackMap{GeoMethod: "latlon", LapsUsed: 17, SessionsUsed: 3}
	got := formatMapLine(19, tm, trackmap.ConfHigh, 1.0, 0)

	want := "Map:     19 segs [latlon] — geometry: high (17 laps, 3 sessions) — match: 100%"
	if !strings.Contains(got, want) {
		t.Errorf("formatMapLine =\n%q\nwant it to contain\n%q", got, want)
	}
}

// TestFormatMapLine_ExistingMapNoMatchScore is the actual regression: a stored
// map with no comparable lap (no valid flying lap, or no track length in the
// YAML) must still report its own confidence, counts and method — not be
// mislabelled as a fresh low-confidence first detection.
func TestFormatMapLine_ExistingMapNoMatchScore(t *testing.T) {
	tm := &trackmap.TrackMap{GeoMethod: "lataccel", LapsUsed: 17, SessionsUsed: 3}
	got := formatMapLine(19, tm, trackmap.ConfHigh, -1, 0)

	if strings.Contains(got, "first detection") {
		t.Errorf("formatMapLine reported a stored map as a first detection:\n%q", got)
	}
	if !strings.Contains(got, "geometry: high") {
		t.Errorf("formatMapLine dropped the stored confidence:\n%q", got)
	}
	if !strings.Contains(got, "17 laps, 3 sessions") {
		t.Errorf("formatMapLine dropped the stored lap/session counts:\n%q", got)
	}
	if !strings.Contains(got, "[lataccel]") {
		t.Errorf("formatMapLine dropped the stored GeoMethod:\n%q", got)
	}
	if !strings.Contains(got, "match: n/a (no comparable lap)") {
		t.Errorf("formatMapLine did not explain the missing match score:\n%q", got)
	}
}

func TestFormatMapLine_FirstDetection(t *testing.T) {
	got := formatMapLine(19, nil, trackmap.ConfLow, -1, 4)

	if !strings.Contains(got, "match: n/a (first detection)") {
		t.Errorf("formatMapLine =\n%q\nwant a first-detection match note", got)
	}
	if !strings.Contains(got, "(4 laps, 1 session)") {
		t.Errorf("formatMapLine =\n%q\nwant the detected lap count", got)
	}
}

// TestFormatMapLine_EmptyGeoMethodDefaults covers maps written before the
// GeoMethod field existed.
func TestFormatMapLine_EmptyGeoMethodDefaults(t *testing.T) {
	tm := &trackmap.TrackMap{LapsUsed: 2, SessionsUsed: 1}
	got := formatMapLine(19, tm, trackmap.ConfModerate, 0.85, 0)

	if !strings.Contains(got, "[latlon]") {
		t.Errorf("formatMapLine =\n%q\nwant [latlon] fallback for an empty GeoMethod", got)
	}
	if !strings.Contains(got, "match: 85%") {
		t.Errorf("formatMapLine =\n%q\nwant match: 85%%", got)
	}
}

func TestFormatMapLine_SingularWording(t *testing.T) {
	tm := &trackmap.TrackMap{GeoMethod: "latlon", LapsUsed: 1, SessionsUsed: 1}
	got := formatMapLine(19, tm, trackmap.ConfLow, 0.5, 0)

	if !strings.Contains(got, "(1 lap, 1 session)") {
		t.Errorf("formatMapLine =\n%q\nwant singular \"1 lap, 1 session\"", got)
	}
}

// ---- pluralize ----

func TestPluralize(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "laps"},
		{1, "lap"},
		{2, "laps"},
	}
	for _, c := range cases {
		if got := pluralize(c.n, "lap", "laps"); got != c.want {
			t.Errorf("pluralize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// ---- parseLapArg ----

func TestParseLapArg(t *testing.T) {
	cases := []struct {
		in       string
		wantMode lapMode
		wantNum  int
		wantErr  bool
	}{
		{"", lapModeBest, 0, false},
		{"0", lapModeBest, 0, false},
		{"pb", lapModePB, 0, false},
		{"PB", lapModePB, 0, false},
		{"  pb  ", lapModePB, 0, false},
		{"3", lapModeNum, 3, false},
		{"banana", 0, 0, true},
	}
	for _, c := range cases {
		mode, num, err := parseLapArg(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseLapArg(%q): want error, got none", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLapArg(%q): unexpected error %v", c.in, err)
			continue
		}
		if mode != c.wantMode || num != c.wantNum {
			t.Errorf("parseLapArg(%q) = (%v, %d), want (%v, %d)", c.in, mode, num, c.wantMode, c.wantNum)
		}
	}
}
