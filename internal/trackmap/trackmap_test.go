package trackmap

import (
	"math"
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
	//   [200,250)  left corner  lat=+15   (50 buckets × 4 m = 200 m)
	//   [250,265)  very short straight lat=0  (15 buckets = 60 m < gap threshold)
	//   [265,315) right corner lat=-15   (50 buckets = 200 m)
	//   [315,1000) straight lat=0
	// Total chicane: 115 buckets × 4 m = 460 m — but the corners themselves are
	// 50+15+50 = 115 buckets = 380 m at this track length, under the 400 m limit.
	n := 1000
	trackLen := 3300.0 // each bucket ≈ 3.3 m → 115 buckets ≈ 380 m (< 400 m max)
	samples := make([]Sample, n)
	for i := 0; i < n; i++ {
		pct := float32(i) / float32(n)
		var lat float32
		switch {
		case i >= 200 && i < 250:
			lat = 15.0
		case i >= 250 && i < 265:
			lat = 0.0
		case i >= 265 && i < 315:
			lat = -15.0
		}
		samples[i] = Sample{LapDistPct: pct, LatAccel: lat}
	}

	segs := Detect(samples, trackLen)
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

	score := MatchScore(samples, segs, 4000.0)
	if score < 0.85 {
		t.Errorf("MatchScore() = %.3f, want >= 0.85", score)
	}
}

func TestMatchScore_NoSegments(t *testing.T) {
	samples := []Sample{{LapDistPct: 0.5, LatAccel: 5.0}}
	score := MatchScore(samples, nil, 4000.0)
	if score != 1.0 {
		t.Errorf("MatchScore(samples, nil) = %.3f, want 1.0", score)
	}
}

func TestMatchScore_SingleSegment(t *testing.T) {
	segs := []Segment{
		{Name: "S1", Kind: KindStraight, EntryPct: 0.0, ExitPct: 1.0},
	}
	samples := []Sample{{LapDistPct: 0.5, LatAccel: 0.0}}
	score := MatchScore(samples, segs, 4000.0)
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

// TestSaveLoad_BrakeEntryRoundtrip verifies that BrakeEntryPct survives a
// Save/Load cycle and that segments without it (zero value) omit the field.
func TestSaveLoad_BrakeEntryRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trackmap.json")

	orig := TrackMapFile{
		"Sebring International Raceway": {
			TrackLengthM: 5793.8,
			Source:       "auto",
			DetectedFrom: "2026-03-27",
			LapsUsed:     15,
			Segments: []Segment{
				{Name: "S1", Kind: KindStraight, EntryPct: 0.0, ExitPct: 0.065},
				{Name: "T1", Kind: KindCorner, EntryPct: 0.065, ExitPct: 0.116, BrakeEntryPct: 0.051},
				{Name: "S2", Kind: KindStraight, EntryPct: 0.116, ExitPct: 0.153},
				{Name: "T2", Kind: KindCorner, EntryPct: 0.153, ExitPct: 0.208, BrakeEntryPct: 0.142},
			},
		},
	}

	if err := Save(path, orig); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tm := loaded["Sebring International Raceway"]

	// Straight: BrakeEntryPct should be zero (omitempty, not serialised).
	if tm.Segments[0].BrakeEntryPct != 0 {
		t.Errorf("S1 BrakeEntryPct: got %v, want 0", tm.Segments[0].BrakeEntryPct)
	}
	// Corner T1: BrakeEntryPct must survive the roundtrip.
	if got := tm.Segments[1].BrakeEntryPct; got != 0.051 {
		t.Errorf("T1 BrakeEntryPct: got %v, want 0.051", got)
	}
	// Corner T2: same.
	if got := tm.Segments[3].BrakeEntryPct; got != 0.142 {
		t.Errorf("T2 BrakeEntryPct: got %v, want 0.142", got)
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

// ---- MatchConfidence / EffectiveConfidence / confidenceRank tests ----

func TestMatchConfidence_High(t *testing.T) {
	if got := MatchConfidence(0.95); got != ConfHigh {
		t.Errorf("MatchConfidence(0.95) = %q, want %q", got, ConfHigh)
	}
}

func TestMatchConfidence_Moderate(t *testing.T) {
	if got := MatchConfidence(0.85); got != ConfModerate {
		t.Errorf("MatchConfidence(0.85) = %q, want %q", got, ConfModerate)
	}
}

func TestMatchConfidence_Low(t *testing.T) {
	if got := MatchConfidence(0.70); got != ConfLow {
		t.Errorf("MatchConfidence(0.70) = %q, want %q", got, ConfLow)
	}
}

func TestMatchConfidence_Boundaries(t *testing.T) {
	// Exactly at the thresholds (≥ 0.93 → high, ≥ 0.80 → moderate).
	if got := MatchConfidence(0.93); got != ConfHigh {
		t.Errorf("MatchConfidence(0.93) = %q, want %q", got, ConfHigh)
	}
	if got := MatchConfidence(0.80); got != ConfModerate {
		t.Errorf("MatchConfidence(0.80) = %q, want %q", got, ConfModerate)
	}
	if got := MatchConfidence(0.79); got != ConfLow {
		t.Errorf("MatchConfidence(0.79) = %q, want %q", got, ConfLow)
	}
}

func TestEffectiveConfidence_TakesLower(t *testing.T) {
	// Map has been seen 20 laps (high geometry confidence) but match score is only moderate.
	// Effective confidence should be moderated down to moderate.
	tm := &TrackMap{LapsUsed: 20}
	eff := tm.EffectiveConfidence(0.85) // match → moderate
	if eff != ConfModerate {
		t.Errorf("EffectiveConfidence = %q, want %q", eff, ConfModerate)
	}
}

func TestEffectiveConfidence_GeometryLimits(t *testing.T) {
	// Map has only 1 lap (low geometry confidence) but match score is perfect.
	// Effective confidence is limited to low by geometry.
	tm := &TrackMap{LapsUsed: 1}
	eff := tm.EffectiveConfidence(0.99) // match → high
	if eff != ConfLow {
		t.Errorf("EffectiveConfidence = %q, want %q", eff, ConfLow)
	}
}

func TestEffectiveConfidence_BothHigh(t *testing.T) {
	tm := &TrackMap{LapsUsed: 20}
	eff := tm.EffectiveConfidence(0.95)
	if eff != ConfHigh {
		t.Errorf("EffectiveConfidence = %q, want %q", eff, ConfHigh)
	}
}

func TestConfidenceRank_Order(t *testing.T) {
	// low < moderate < high
	if confidenceRank(ConfLow) >= confidenceRank(ConfModerate) {
		t.Error("low rank should be less than moderate rank")
	}
	if confidenceRank(ConfModerate) >= confidenceRank(ConfHigh) {
		t.Error("moderate rank should be less than high rank")
	}
}

// ---- AddSession cap test ----

func TestAddSession_CapAt50(t *testing.T) {
	tm := &TrackMap{}
	for i := 0; i < 60; i++ {
		tm.AddSession(string(rune('A' + i%26)) + string(rune('0'+i%10)))
	}
	if len(tm.SeenSessions) > maxSeenSessions {
		t.Errorf("SeenSessions len = %d, want <= %d", len(tm.SeenSessions), maxSeenSessions)
	}
}

// ---- project / signedCurvature unit tests ----

func TestProject_Origin(t *testing.T) {
	x, y := project(37.0, -121.0, 37.0, -121.0)
	if x != 0 || y != 0 {
		t.Errorf("project at origin: got (%v,%v), want (0,0)", x, y)
	}
}

func TestProject_NorthSouth(t *testing.T) {
	// Moving 1 degree north should give roughly 111 km north.
	_, y := project(38.0, -121.0, 37.0, -121.0)
	if math.Abs(y-111000) > 2000 { // within 2 km of expected
		t.Errorf("project 1° north: y = %.0f m, want ~111000 m", y)
	}
}

func TestSignedCurvature_Straight(t *testing.T) {
	// Three collinear points → zero curvature.
	k := signedCurvature(0, 0, 1, 0, 2, 0)
	if k != 0 {
		t.Errorf("collinear curvature = %v, want 0", k)
	}
}

func TestSignedCurvature_LeftTurn(t *testing.T) {
	// Points going along a unit circle (radius = 1 m) in CCW (left-turn) order:
	// A=(1,0), B=(0,1), C=(-1,0) — 90° arc, radius 1 m → κ = 1/r = 1.0 m⁻¹, positive.
	k := signedCurvature(1, 0, 0, 1, -1, 0)
	if k <= 0 {
		t.Errorf("left turn curvature = %v, want positive", k)
	}
	if math.Abs(float64(k)-1.0) > 0.1 {
		t.Errorf("left turn curvature = %v, want ~1.0 m⁻¹", k)
	}
}

func TestSignedCurvature_RightTurn(t *testing.T) {
	// Reverse order → right turn → negative curvature.
	k := signedCurvature(-1, 0, 0, 1, 1, 0)
	if k >= 0 {
		t.Errorf("right turn curvature = %v, want negative", k)
	}
}

// ---- DetectFromMultipleLatLon tests ----

// makeOvalSamples builds a synthetic lap around an elongated oval (semi-major 500m,
// semi-minor 50m) centred at the given lat/lon. The oval has high curvature at the
// ends (κ ≈ 0.2 m⁻¹) and very low curvature on the sides (κ ≈ 0.0002 m⁻¹), which
// should produce two corners and two straights when analysed.
func makeOvalSamples(n int, lat0, lon0 float64) []Sample {
	const (
		semiMajor = 500.0 // metres, along y-axis (lat)
		semiMinor = 50.0  // metres, along x-axis (lon)
		// 1 degree lat ≈ 111 km; 1 degree lon at lat0 ≈ 111 km * cos(lat0)
		earthRadius = 6_371_000.0
	)
	degPerMetreLat := 180.0 / (math.Pi * earthRadius)
	degPerMetreLon := degPerMetreLat / math.Cos(lat0*math.Pi/180.0)

	samples := make([]Sample, n)
	for i := 0; i < n; i++ {
		t := 2 * math.Pi * float64(i) / float64(n)
		xM := semiMinor * math.Sin(t)  // x in metres
		yM := semiMajor * math.Cos(t)  // y in metres
		samples[i] = Sample{
			LapDistPct: float32(i) / float32(n),
			LatAccel:   0, // not used by latlon path
			Lat:        lat0 + yM*degPerMetreLat,
			Lon:        lon0 + xM*degPerMetreLon,
		}
	}
	return samples
}

// Approximate perimeter of the oval (used as trackLengthM).
func ovalPerimeterM() float64 {
	a, b := 500.0, 50.0
	// Ramanujan approximation
	return math.Pi * (3*(a+b) - math.Sqrt((3*a+b)*(a+3*b)))
}

func TestDetectFromMultipleLatLon_NoLatLon(t *testing.T) {
	// All samples have Lat=0, Lon=0 → function must return nil (no GPS data).
	samples := make([]Sample, 100)
	for i := range samples {
		samples[i] = Sample{LapDistPct: float32(i) / 100, LatAccel: 5.0}
	}
	segs := DetectFromMultipleLatLon([][]Sample{samples}, 4000.0, 0)
	if segs != nil {
		t.Errorf("expected nil (no lat/lon), got %d segments", len(segs))
	}
}

func TestDetectFromMultipleLatLon_EmptyInput(t *testing.T) {
	segs := DetectFromMultipleLatLon(nil, 4000.0, 0)
	if segs != nil {
		t.Errorf("expected nil for empty input, got %d segments", len(segs))
	}
}

func TestDetectFromMultipleLatLon_OvalProducesSegments(t *testing.T) {
	samples := makeOvalSamples(1000, 37.0, -121.0)
	trackLen := ovalPerimeterM()
	segs := DetectFromMultipleLatLon([][]Sample{samples}, trackLen, 0)
	if len(segs) == 0 {
		t.Fatal("DetectFromMultipleLatLon: expected segments from oval, got none")
	}
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
		t.Errorf("expected corners from oval, got 0 (segs: %v)", segs)
	}
	if straights == 0 {
		t.Errorf("expected straights from oval, got 0 (segs: %v)", segs)
	}
}

func TestDetectFromMultipleLatLon_MultiLap(t *testing.T) {
	// Two identical laps should produce the same result as one.
	samples := makeOvalSamples(1000, 37.0, -121.0)
	trackLen := ovalPerimeterM()

	segs1 := DetectFromMultipleLatLon([][]Sample{samples}, trackLen, 0)
	segs2 := DetectFromMultipleLatLon([][]Sample{samples, samples}, trackLen, 0)

	if len(segs1) == 0 {
		t.Skip("no segments detected for single lap — oval test data may be borderline")
	}
	if len(segs1) != len(segs2) {
		t.Errorf("single-lap segs=%d, two-lap segs=%d (should match)", len(segs1), len(segs2))
	}
}

// ---------------------------------------------------------------------------
// Post-detection validation tests
// ---------------------------------------------------------------------------

// TestTrimWraparoundCorner_RemovesTinyCornerAtStart verifies that a small
// GPS-artifact corner at bucket 0 is merged into the following straight.
func TestTrimWraparoundCorner_RemovesTinyCornerAtStart(t *testing.T) {
	segs := []rawSeg{
		{isCorner: true, start: 0, end: 4, latSign: 0.5},   // 5 buckets = tiny
		{isCorner: false, start: 5, end: 100, latSign: 0},   // straight
		{isCorner: true, start: 101, end: 200, latSign: -1},  // real corner
		{isCorner: false, start: 201, end: 999, latSign: 0},  // straight
	}
	result := trimWraparoundCorner(segs, 3000.0) // 5 buckets × 3m = 15m < 50m threshold
	// First segment should now be a straight (the tiny corner was merged).
	if len(result) < 1 {
		t.Fatal("expected segments, got none")
	}
	if result[0].isCorner {
		t.Error("expected first segment to be a straight after trimming GPS artifact")
	}
}

// TestTrimWraparoundCorner_PreservesLargeCorner verifies that a real corner
// at the start of the track (longer than the threshold) is preserved.
func TestTrimWraparoundCorner_PreservesLargeCorner(t *testing.T) {
	segs := []rawSeg{
		{isCorner: true, start: 0, end: 80, latSign: 0.5},  // 81 buckets ≈ 243m > 50m
		{isCorner: false, start: 81, end: 999, latSign: 0},
	}
	result := trimWraparoundCorner(segs, 3000.0)
	if !result[0].isCorner {
		t.Error("large corner at start should be preserved")
	}
}

// TestConfirmCorners_RejectsNoSteerNoLatG verifies that a corner with neither
// meaningful steering nor lateral G is reclassified as a straight.
func TestConfirmCorners_RejectsNoSteerNoLatG(t *testing.T) {
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 200, latSign: 1.0}, // "corner" with no real data
		{isCorner: false, start: 201, end: 999, latSign: 0},
	}
	// Profiles: near-zero steering and near-zero lateral G in the corner region.
	steerAvg := make([]float64, numBuckets)
	latAccAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		steerAvg[i] = 0.01 // ~0.6 degrees — well below 10° threshold
		latAccAvg[i] = 0.5 // well below 2.0 m/s²
	}

	result := confirmCorners(segs, steerAvg, latAccAvg, 3000.0)
	for _, s := range result {
		if s.isCorner && s.start <= 200 && s.end >= 100 {
			t.Error("expected false corner to be reclassified as straight")
		}
	}
}

// TestConfirmCorners_KeepsRealCorner verifies that a corner with meaningful
// steering is preserved even if lateral G is low.
func TestConfirmCorners_KeepsRealCorner(t *testing.T) {
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 200, latSign: 1.0},
		{isCorner: false, start: 201, end: 999, latSign: 0},
	}
	steerAvg := make([]float64, numBuckets)
	latAccAvg := make([]float64, numBuckets)
	for i := 0; i < numBuckets; i++ {
		latAccAvg[i] = 0.5 // low lat-G
	}
	// Strong steering in the corner zone.
	for i := 100; i <= 200; i++ {
		steerAvg[i] = 0.35 // ~20 degrees — well above threshold
	}

	result := confirmCorners(segs, steerAvg, latAccAvg, 3000.0)
	hasCorner := false
	for _, s := range result {
		if s.isCorner && s.start >= 80 && s.end <= 220 {
			hasCorner = true
		}
	}
	if !hasCorner {
		t.Error("real corner with strong steering should be preserved")
	}
}

// TestValidateCornerSpeed_FlatSpeedReclassified verifies that a "corner" with
// flat speed throughout is reclassified as a straight.
func TestValidateCornerSpeed_FlatSpeedReclassified(t *testing.T) {
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 200, latSign: 1.0},
		{isCorner: false, start: 201, end: 999, latSign: 0},
	}
	speedAvg := make([]float64, numBuckets)
	for i := range speedAvg {
		speedAvg[i] = 50.0 // constant 50 m/s everywhere
	}

	result := validateCornerSpeed(segs, speedAvg, 3000.0)
	for _, s := range result {
		if s.isCorner && s.start <= 200 && s.end >= 100 {
			t.Error("corner with flat speed should be reclassified as straight")
		}
	}
}

// TestValidateCornerSpeed_KeepsRealCorner verifies that a corner with a clear
// speed drop is preserved.
func TestValidateCornerSpeed_KeepsRealCorner(t *testing.T) {
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 200, latSign: 1.0},
		{isCorner: false, start: 201, end: 999, latSign: 0},
	}
	speedAvg := make([]float64, numBuckets)
	for i := range speedAvg {
		speedAvg[i] = 50.0
	}
	// Create a speed dip in the corner (50 → 30 → 50 m/s).
	for i := 130; i <= 170; i++ {
		speedAvg[i] = 30.0
	}

	result := validateCornerSpeed(segs, speedAvg, 3000.0)
	hasCorner := false
	for _, s := range result {
		if s.isCorner && s.start >= 80 && s.end <= 220 {
			hasCorner = true
		}
	}
	if !hasCorner {
		t.Error("corner with clear speed drop should be preserved")
	}
}

// TestSplitLargeCorners_TwoTroughs verifies that a long corner with two
// distinct speed troughs is split into two separate corners.
func TestSplitLargeCorners_TwoTroughs(t *testing.T) {
	// Single large corner spanning 400 buckets on a 3000m track = 1200m.
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 500, latSign: 1.0},
		{isCorner: false, start: 501, end: 999, latSign: 0},
	}
	speedAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	for i := range speedAvg {
		speedAvg[i] = 50.0
		signAvg[i] = 0.0
	}
	// Two speed troughs at buckets 200 and 400, with a peak at 300.
	for i := 100; i <= 500; i++ {
		signAvg[i] = 1.0
	}
	for i := 150; i <= 250; i++ {
		speedAvg[i] = 50.0 - 20.0*math.Abs(float64(i-200)/50.0) // trough at 200: min=30
		if speedAvg[i] < 30.0 {
			speedAvg[i] = 30.0
		}
	}
	for i := 250; i <= 350; i++ {
		speedAvg[i] = 50.0 // peak: re-acceleration to 50 m/s
	}
	for i := 350; i <= 450; i++ {
		speedAvg[i] = 50.0 - 20.0*math.Abs(float64(i-400)/50.0) // trough at 400: min=30
		if speedAvg[i] < 30.0 {
			speedAvg[i] = 30.0
		}
	}

	result := splitLargeCorners(segs, speedAvg, signAvg, 3000.0)
	cornerCount := 0
	for _, s := range result {
		if s.isCorner {
			cornerCount++
		}
	}
	if cornerCount < 2 {
		t.Errorf("expected at least 2 corners after splitting, got %d", cornerCount)
		for _, s := range result {
			kind := "straight"
			if s.isCorner {
				kind = "corner"
			}
			t.Logf("  %s [%d-%d]", kind, s.start, s.end)
		}
	}
}

// TestSplitLargeCorners_SingleTrough verifies that a long corner with only
// one speed trough is NOT split.
func TestSplitLargeCorners_SingleTrough(t *testing.T) {
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 500, latSign: 1.0},
		{isCorner: false, start: 501, end: 999, latSign: 0},
	}
	speedAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	for i := range speedAvg {
		speedAvg[i] = 50.0
	}
	// Single trough at bucket 300: 50 → 30 → 50.
	for i := 200; i <= 400; i++ {
		speedAvg[i] = 50.0 - 20.0*(1.0-math.Abs(float64(i-300)/100.0))
		if speedAvg[i] > 50.0 {
			speedAvg[i] = 50.0
		}
	}
	for i := 100; i <= 500; i++ {
		signAvg[i] = 1.0
	}

	result := splitLargeCorners(segs, speedAvg, signAvg, 3000.0)
	cornerCount := 0
	for _, s := range result {
		if s.isCorner {
			cornerCount++
		}
	}
	if cornerCount != 1 {
		t.Errorf("expected 1 corner (no split for single trough), got %d", cornerCount)
	}
}

// TestMergeChicanes_RejectsOversizedChicane verifies that two corners
// separated by a short straight are NOT merged if the total length exceeds
// the chicane maximum.
func TestMergeChicanes_RejectsOversizedChicane(t *testing.T) {
	// Two 200-bucket corners with a 10-bucket gap on a 2000m track.
	// Total = 410 buckets × 2m = 820m >> 400m limit.
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 300, latSign: 1.0},  // 201 buckets
		{isCorner: false, start: 301, end: 310, latSign: 0},    // 10-bucket gap
		{isCorner: true, start: 311, end: 510, latSign: -1.0},  // 200 buckets
		{isCorner: false, start: 511, end: 999, latSign: 0},
	}

	result := mergeChicanes(segs, 2000.0)
	for _, s := range result {
		if s.isChicane {
			t.Error("oversized corner pair should not be merged into a chicane")
		}
	}
}

// TestLoadTrackRef_Missing verifies that LoadTrackRef returns an empty map
// (not error) for a nonexistent file.
func TestLoadTrackRef_Missing(t *testing.T) {
	trf, err := LoadTrackRef("nonexistent_trackref_xyzzy.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trf) != 0 {
		t.Errorf("expected empty map, got %d entries", len(trf))
	}
}

// TestLoadTrackRef_Roundtrip verifies LoadTrackRef reads a valid JSON file.
func TestLoadTrackRef_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trackref.json")
	data := []byte(`{"Test Track":{"corners":5,"comment":"test"}}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	trf, err := LoadTrackRef(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n, ok := trf.Corners("Test Track")
	if !ok || n != 5 {
		t.Errorf("expected 5 corners for 'Test Track', got %d (ok=%v)", n, ok)
	}
	_, ok = trf.Corners("Unknown Track")
	if ok {
		t.Error("expected false for unknown track")
	}
}

// TestSearchThresholds_MatchesTarget verifies that the threshold search
// produces the expected corner count when given a target.
func TestSearchThresholds_MatchesTarget(t *testing.T) {
	// Build a synthetic curvature profile with 3 clear corners.
	absAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	counts := make([]int, numBuckets)
	for i := range counts {
		counts[i] = 1
	}

	// Three corner regions with strong curvature.
	corners := [][2]int{{100, 200}, {400, 500}, {700, 800}}
	for _, c := range corners {
		for i := c[0]; i < c[1]; i++ {
			absAvg[i] = 0.01 // well above default enter threshold
			signAvg[i] = 1.0
		}
	}

	// Identity post-processing (no-op).
	noop := func(segs []rawSeg) []rawSeg { return segs }

	result := searchThresholds(absAvg, signAvg, counts, 4000.0, 3, noop)
	got := countCornerSegs(result)
	if got != 3 {
		t.Errorf("expected 3 corners, got %d", got)
	}
}

// TestRefineBoundaries_AbsorbsFalseStraight verifies that a short "straight"
// with active cornering (high steering) between two corners gets absorbed.
func TestRefineBoundaries_AbsorbsFalseStraight(t *testing.T) {
	// Two corners with a 10-bucket "straight" gap that has high steering.
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 200, latSign: 1.0},
		{isCorner: false, start: 201, end: 210, latSign: 0}, // 10-bucket "straight"
		{isCorner: true, start: 211, end: 300, latSign: -1.0},
		{isCorner: false, start: 301, end: 999, latSign: 0},
	}

	steerAvg := make([]float64, numBuckets)
	latAccAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	// High steering through the "straight" gap.
	for i := 100; i <= 300; i++ {
		steerAvg[i] = 0.30 // ~17 degrees
	}

	result := refineBoundaries(segs, steerAvg, latAccAvg, signAvg, 3000.0)
	// The false straight should be absorbed — we should have fewer segments.
	straightCount := 0
	for _, s := range result {
		if !s.isCorner && s.start >= 100 && s.end <= 300 {
			straightCount++
		}
	}
	if straightCount > 0 {
		t.Error("false straight with high steering should have been absorbed by adjacent corners")
		for _, s := range result {
			kind := "straight"
			if s.isCorner {
				kind = "corner"
			}
			t.Logf("  %s [%d-%d]", kind, s.start, s.end)
		}
	}
}

// TestRefineBoundaries_ShrinksCornerWithStraightEntry verifies that a corner
// whose leading buckets have no cornering activity gets trimmed.
func TestRefineBoundaries_ShrinksCornerWithStraightEntry(t *testing.T) {
	segs := []rawSeg{
		{isCorner: false, start: 0, end: 99, latSign: 0},
		{isCorner: true, start: 100, end: 300, latSign: 1.0},
		{isCorner: false, start: 301, end: 999, latSign: 0},
	}

	steerAvg := make([]float64, numBuckets)
	latAccAvg := make([]float64, numBuckets)
	signAvg := make([]float64, numBuckets)
	// No steering in the first 50 buckets of the corner (100-149).
	// Strong steering only from bucket 150 onwards.
	for i := 150; i <= 300; i++ {
		steerAvg[i] = 0.30
	}

	result := refineBoundaries(segs, steerAvg, latAccAvg, signAvg, 3000.0)
	// The corner should start later (around 150, not 100).
	for _, s := range result {
		if s.isCorner {
			if s.start < 140 {
				t.Errorf("expected corner start to shift right (to ~150), got %d", s.start)
			}
			break
		}
	}
}
