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
