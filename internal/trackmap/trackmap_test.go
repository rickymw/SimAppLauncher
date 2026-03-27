package trackmap

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetect_ProducesStraightsAndCorners builds a synthetic sample set with
// alternating straight (LatAccel≈0) and corner (LatAccel=15) sections and
// verifies that Detect returns segments of alternating kinds with the right count.
func TestDetect_ProducesStraightsAndCorners(t *testing.T) {
	// Build 2000 samples: 4 sections of 500 each, alternating straight/corner.
	// Sections: [0,500) straight, [500,1000) corner, [1000,1500) straight, [1500,2000) corner.
	n := 2000
	samples := make([]Sample, n)
	for i := 0; i < n; i++ {
		pct := float32(i) / float32(n)
		section := int(pct * 4)
		var lat float32
		if section%2 == 1 {
			lat = 15.0 // well above enter threshold of 5.0
		}
		samples[i] = Sample{LapDistPct: pct, LatAccel: lat}
	}

	segs := Detect(samples, 4000.0)
	if len(segs) == 0 {
		t.Fatal("expected at least one segment, got none")
	}

	// Count corners and straights.
	var corners, straights int
	for _, s := range segs {
		switch s.Kind {
		case KindCorner, KindChicane:
			corners++
		case KindStraight:
			straights++
		}
	}

	if corners == 0 {
		t.Error("expected at least one corner, got 0")
	}
	if straights == 0 {
		t.Error("expected at least one straight, got 0")
	}

	// Verify alternating: first segment kind should differ from second.
	if len(segs) >= 2 {
		k1, k2 := segs[0].Kind, segs[1].Kind
		isCorner1 := k1 == KindCorner || k1 == KindChicane
		isCorner2 := k2 == KindCorner || k2 == KindChicane
		if isCorner1 == isCorner2 {
			t.Errorf("expected alternating straight/corner, got %v then %v", k1, k2)
		}
	}
}

// TestDetect_EmptySamples verifies that Detect(nil, 6000) returns nil without panic.
func TestDetect_EmptySamples(t *testing.T) {
	segs := Detect(nil, 6000)
	if segs != nil {
		t.Errorf("expected nil, got %v", segs)
	}
}

// TestDetect_ChicaneDetection builds samples with left corner, short straight,
// right corner (LatAccel flips sign) and verifies a chicane is detected.
func TestDetect_ChicaneDetection(t *testing.T) {
	// Build 1000 samples (1:1 with buckets) with pattern:
	//   [0,200)   straight  lat=0
	//   [200,350)  left corner  lat=+15
	//   [350,365)  very short straight lat=0  (15 buckets < threshold of 18)
	//   [365,500) right corner lat=-15
	//   [500,1000) straight lat=0
	n := 1000
	samples := make([]Sample, n)
	for i := 0; i < n; i++ {
		pct := float32(i) / float32(n)
		var lat float32
		switch {
		case i >= 200 && i < 350:
			lat = 15.0
		case i >= 350 && i < 365:
			lat = 0.0
		case i >= 365 && i < 500:
			lat = -15.0
		}
		samples[i] = Sample{LapDistPct: pct, LatAccel: lat}
	}

	segs := Detect(samples, 4000.0)
	if len(segs) == 0 {
		t.Fatal("expected segments, got none")
	}

	var hasChicane bool
	for _, s := range segs {
		if s.Kind == KindChicane {
			hasChicane = true
			break
		}
	}
	if !hasChicane {
		t.Log("segments detected:")
		for _, s := range segs {
			t.Logf("  %s (%s) %.1f%%–%.1f%%", s.Name, s.Kind, s.EntryPct*100, s.ExitPct*100)
		}
		t.Error("expected a chicane segment to be detected")
	}
}

// TestLoad_FileNotFound verifies Load returns empty map and nil error when file is missing.
func TestLoad_FileNotFound(t *testing.T) {
	tmf, err := Load("nonexistent_trackmap_xyzzy.json")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(tmf) != 0 {
		t.Errorf("expected empty map, got %v", tmf)
	}
}

// ---- Confidence tests ----

func TestConfidence_Low(t *testing.T) {
	tm := &TrackMap{LapsUsed: 1}
	if got := tm.Confidence(); got != ConfLow {
		t.Errorf("Confidence() = %q, want %q", got, ConfLow)
	}
}

func TestConfidence_Moderate(t *testing.T) {
	tm := &TrackMap{LapsUsed: 5}
	if got := tm.Confidence(); got != ConfModerate {
		t.Errorf("Confidence() = %q, want %q", got, ConfModerate)
	}
}

func TestConfidence_High(t *testing.T) {
	tm := &TrackMap{LapsUsed: 15}
	if got := tm.Confidence(); got != ConfHigh {
		t.Errorf("Confidence() = %q, want %q", got, ConfHigh)
	}
}

// ---- MatchScore tests ----

// TestMatchScore_PerfectMatch builds samples that match the stored segments
// exactly (LatAccel=15 in corner buckets, 0 in straight buckets).
// Expects a score >= 0.85.
func TestMatchScore_PerfectMatch(t *testing.T) {
	// Segments: S1 [0.0, 0.25), T1 [0.25, 0.50), S2 [0.50, 0.75), T2 [0.75, 1.0)
	segs := []Segment{
		{Name: "S1", Kind: KindStraight, EntryPct: 0.00, ExitPct: 0.25},
		{Name: "T1", Kind: KindCorner, EntryPct: 0.25, ExitPct: 0.50},
		{Name: "S2", Kind: KindStraight, EntryPct: 0.50, ExitPct: 0.75},
		{Name: "T2", Kind: KindCorner, EntryPct: 0.75, ExitPct: 1.00},
	}

	// Build 1000 samples (1 per bucket) with LatAccel=15 in corner regions,
	// 0 in straight regions.
	n := 1000
	samples := make([]Sample, n)
	for i := 0; i < n; i++ {
		pct := float32(i) / float32(n)
		var lat float32
		// Corner buckets: [250,500) and [750,1000)
		if (i >= 250 && i < 500) || (i >= 750 && i < 1000) {
			lat = 15.0
		}
		samples[i] = Sample{LapDistPct: pct, LatAccel: lat}
	}

	score := MatchScore(samples, segs)
	if score < 0.85 {
		t.Errorf("MatchScore() = %.3f, want >= 0.85", score)
	}
}

func TestMatchScore_NoSegments(t *testing.T) {
	samples := []Sample{{LapDistPct: 0.5, LatAccel: 5.0}}
	score := MatchScore(samples, nil)
	if score != 1.0 {
		t.Errorf("MatchScore(samples, nil) = %.3f, want 1.0", score)
	}
}

func TestMatchScore_SingleSegment(t *testing.T) {
	segs := []Segment{
		{Name: "S1", Kind: KindStraight, EntryPct: 0.0, ExitPct: 1.0},
	}
	samples := []Sample{{LapDistPct: 0.5, LatAccel: 0.0}}
	score := MatchScore(samples, segs)
	if score != 1.0 {
		t.Errorf("MatchScore(samples, oneSegment) = %.3f, want 1.0", score)
	}
}

// ---- HasSession / AddSession tests ----

// TestHasSession_NotPresent verifies HasSession returns false for an empty SeenSessions slice.
func TestHasSession_NotPresent(t *testing.T) {
	tm := &TrackMap{}
	if tm.HasSession("2026-03-25T16:32:28Z") {
		t.Error("HasSession on empty SeenSessions should return false")
	}
}

// TestAddSession_AddsAndDeduplicates verifies AddSession stores an ID and is idempotent.
func TestAddSession_AddsAndDeduplicates(t *testing.T) {
	tm := &TrackMap{}
	id := "2026-03-25T16:32:28Z"
	tm.AddSession(id)
	tm.AddSession(id) // second call should be a no-op
	if !tm.HasSession(id) {
		t.Error("HasSession should return true after AddSession")
	}
	if len(tm.SeenSessions) != 1 {
		t.Errorf("SeenSessions should have 1 entry after duplicate AddSession, got %d", len(tm.SeenSessions))
	}
}

// ---- DetectFromMultiple tests ----

// TestDetectFromMultiple_MatchesSingleLap verifies that DetectFromMultiple with one
// lap produces the same segments as Detect with those same samples.
func TestDetectFromMultiple_MatchesSingleLap(t *testing.T) {
	n := 2000
	samples := make([]Sample, n)
	for i := 0; i < n; i++ {
		pct := float32(i) / float32(n)
		section := int(pct * 4)
		var lat float32
		if section%2 == 1 {
			lat = 15.0
		}
		samples[i] = Sample{LapDistPct: pct, LatAccel: lat}
	}

	segsDetect := Detect(samples, 4000.0)
	segsMulti := DetectFromMultiple([][]Sample{samples}, 4000.0)

	if len(segsDetect) != len(segsMulti) {
		t.Errorf("segment count mismatch: Detect=%d DetectFromMultiple=%d", len(segsDetect), len(segsMulti))
		return
	}
	for i := range segsDetect {
		if segsDetect[i].Kind != segsMulti[i].Kind {
			t.Errorf("seg[%d] kind: Detect=%s DetectFromMultiple=%s", i, segsDetect[i].Kind, segsMulti[i].Kind)
		}
		if segsDetect[i].EntryPct != segsMulti[i].EntryPct {
			t.Errorf("seg[%d] entryPct: Detect=%v DetectFromMultiple=%v", i, segsDetect[i].EntryPct, segsMulti[i].EntryPct)
		}
	}
}

// TestDetectFromMultiple_MultiLap verifies that DetectFromMultiple with two consistent
// laps produces the same segment count as single-lap detection.
func TestDetectFromMultiple_MultiLap(t *testing.T) {
	// Build two laps with the same corner positions but slight noise variation.
	makeLap := func(noise float32) []Sample {
		n := 2000
		s := make([]Sample, n)
		for i := 0; i < n; i++ {
			pct := float32(i) / float32(n)
			section := int(pct * 4)
			var lat float32
			if section%2 == 1 {
				lat = 15.0 + noise
			}
			s[i] = Sample{LapDistPct: pct, LatAccel: lat}
		}
		return s
	}

	lap1 := makeLap(0)
	lap2 := makeLap(0.5)

	segsSingle := Detect(lap1, 4000.0)
	segsMulti := DetectFromMultiple([][]Sample{lap1, lap2}, 4000.0)

	if len(segsMulti) == 0 {
		t.Fatal("DetectFromMultiple returned no segments")
	}
	if len(segsSingle) != len(segsMulti) {
		t.Errorf("segment count: single=%d multi=%d (expected equal)", len(segsSingle), len(segsMulti))
	}
}

// TestSaveLoad_Roundtrip saves a TrackMapFile and loads it back, verifying all fields survive.
func TestSaveLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trackmap.json")

	orig := TrackMapFile{
		"Sebring International Raceway": {
			TrackLengthM: 5954.0,
			Source:       "auto",
			DetectedFrom: "2026-03-27",
			LapsUsed:     3,
			Segments: []Segment{
				{Name: "S1", Kind: KindStraight, EntryPct: 0.0, ExitPct: 0.05, EntryM: 0, ExitM: 297.7},
				{Name: "T1", Kind: KindCorner, EntryPct: 0.05, ExitPct: 0.12, EntryM: 297.7, ExitM: 714.48},
			},
		},
	}

	if err := Save(path, orig); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file was created.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	tm, ok := loaded["Sebring International Raceway"]
	if !ok {
		t.Fatal("track not found in loaded map")
	}

	if tm.TrackLengthM != 5954.0 {
		t.Errorf("TrackLengthM = %v, want 5954.0", tm.TrackLengthM)
	}
	if tm.Source != "auto" {
		t.Errorf("Source = %q, want %q", tm.Source, "auto")
	}
	if tm.LapsUsed != 3 {
		t.Errorf("LapsUsed = %d, want 3", tm.LapsUsed)
	}
	if len(tm.Segments) != 2 {
		t.Fatalf("len(Segments) = %d, want 2", len(tm.Segments))
	}
	if tm.Segments[0].Name != "S1" {
		t.Errorf("Segments[0].Name = %q, want S1", tm.Segments[0].Name)
	}
	if tm.Segments[1].Kind != KindCorner {
		t.Errorf("Segments[1].Kind = %q, want corner", tm.Segments[1].Kind)
	}
}
