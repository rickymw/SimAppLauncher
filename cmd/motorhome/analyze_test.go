package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
)

// makeLap builds a minimal analysis.Lap for bestAnalyzeLap testing.
func makeLap(kind analysis.LapKind, lapTime float32, samples int, partialStart bool) analysis.Lap {
	s := make([]analysis.SampleData, samples)
	return analysis.Lap{
		Kind:           kind,
		LapTime:        lapTime,
		IsPartialStart: partialStart,
		Samples:        s,
	}
}

func TestBestAnalyzeLap_UsesLapTime(t *testing.T) {
	// Lap 1: slower lap but fewer samples (old proxy would have picked this).
	// Lap 2: faster lap but more samples.
	// bestAnalyzeLap must select Lap 2 (lower LapTime), not Lap 1.
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 120.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 115.0, analysis.MinSamplesForValidLap+100, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil, want fastest lap")
	}
	if got.LapTime != 115.0 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 115.0 (fastest lap must be selected by LapTime)", got.LapTime)
	}
}

func TestBestAnalyzeLap_ExcludesPartialStart(t *testing.T) {
	// Lap 1: fastest, but started mid-recording — must be excluded.
	// Lap 2: slower, but a clean full lap.
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 110.0, analysis.MinSamplesForValidLap, true),
		makeLap(analysis.KindFlying, 115.0, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil, want non-partial-start lap")
	}
	if got.LapTime != 115.0 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 115.0 (partial-start lap must be excluded)", got.LapTime)
	}
}

func TestBestAnalyzeLap_ExcludesNonFlying(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindOutLap, 100.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindInLap, 100.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 120.0, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil, want flying lap")
	}
	if got.Kind != analysis.KindFlying {
		t.Errorf("bestAnalyzeLap: Kind = %v, want flying", got.Kind)
	}
}

func TestBestAnalyzeLap_ExcludesZeroLapTime(t *testing.T) {
	// A flying lap with LapTime == 0 must be skipped (timing data is missing).
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 120.0, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil")
	}
	if got.LapTime != 120.0 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 120.0 (zero LapTime must be excluded)", got.LapTime)
	}
}

func TestBestAnalyzeLap_ExcludesBelowMinSamples(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 100.0, analysis.MinSamplesForValidLap-1, false),
		makeLap(analysis.KindFlying, 120.0, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil")
	}
	if got.LapTime != 120.0 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 120.0 (under-sample lap must be excluded)", got.LapTime)
	}
}

func TestBestAnalyzeLap_AllExcluded(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindOutLap, 100.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 100.0, analysis.MinSamplesForValidLap, true), // partial start
	}
	got := bestAnalyzeLap(laps)
	if got != nil {
		t.Errorf("bestAnalyzeLap: got non-nil, want nil when no valid laps exist")
	}
}

// TestBestAnalyzeLap_RejectsImplausiblyShortLap reproduces the Laguna Seca
// case where iRacing published LLT=36.65 for a phantom flying lap; the real
// flying laps were ~90s. The 36.65s lap must not be picked as best.
func TestBestAnalyzeLap_RejectsImplausiblyShortLap(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 36.650, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 91.216, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 90.614, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 90.465, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 90.093, analysis.MinSamplesForValidLap, false),
	}
	got := bestAnalyzeLap(laps)
	if got == nil {
		t.Fatal("bestAnalyzeLap: got nil, want fastest plausible lap")
	}
	if got.LapTime != 90.093 {
		t.Errorf("bestAnalyzeLap: LapTime = %v, want 90.093 (36.650 lap is implausibly short and must be rejected)", got.LapTime)
	}
}

// TestPlausibleLapMinTime_NoFloorWithSingleLap ensures we don't over-filter
// when the session has only one usable flying lap (no median to compare to).
func TestPlausibleLapMinTime_NoFloorWithSingleLap(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindOutLap, 100.0, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 90.0, analysis.MinSamplesForValidLap, false),
	}
	got := plausibleLapMinTime(laps)
	if got != 0 {
		t.Errorf("plausibleLapMinTime with one flying lap = %v, want 0 (insufficient data for floor)", got)
	}
}

// TestFlyingLapsWithinTime_ExcludesImplausiblyShort ensures the trackmap
// detection input doesn't include a phantom short lap even though it's within
// the +1.5s window of any reasonable best lap.
func TestFlyingLapsWithinTime_ExcludesImplausiblyShort(t *testing.T) {
	laps := []analysis.Lap{
		makeLap(analysis.KindFlying, 36.650, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 90.093, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 90.465, analysis.MinSamplesForValidLap, false),
		makeLap(analysis.KindFlying, 90.614, analysis.MinSamplesForValidLap, false),
	}
	got := flyingLapsWithinTime(laps, 90.093)
	if len(got) == 0 {
		t.Fatal("flyingLapsWithinTime: got 0 laps, want plausible laps near best")
	}
	for _, l := range got {
		if l.LapTime < 60.0 {
			t.Errorf("flyingLapsWithinTime: included implausible lap with LapTime=%v", l.LapTime)
		}
	}
}

// ---- nthLatestIbtFile tests ----

// createIbtFiles creates n fake .ibt files in dir with 1-second apart mod times.
// Returns paths in creation order (oldest first).
func createIbtFiles(t *testing.T, dir string, names []string) {
	t.Helper()
	base := time.Now().Add(-time.Duration(len(names)) * time.Second)
	for i, name := range names {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("fake"), 0644); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
		mt := base.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
	}
}

func TestNthLatestIbtFile_MostRecent(t *testing.T) {
	dir := t.TempDir()
	createIbtFiles(t, dir, []string{"old.ibt", "mid.ibt", "new.ibt"})

	got, err := nthLatestIbtFile(dir, 1)
	if err != nil {
		t.Fatalf("nthLatestIbtFile(1): %v", err)
	}
	if filepath.Base(got) != "new.ibt" {
		t.Errorf("nthLatestIbtFile(1) = %q, want new.ibt", filepath.Base(got))
	}
}

func TestNthLatestIbtFile_SecondMostRecent(t *testing.T) {
	dir := t.TempDir()
	createIbtFiles(t, dir, []string{"old.ibt", "mid.ibt", "new.ibt"})

	got, err := nthLatestIbtFile(dir, 2)
	if err != nil {
		t.Fatalf("nthLatestIbtFile(2): %v", err)
	}
	if filepath.Base(got) != "mid.ibt" {
		t.Errorf("nthLatestIbtFile(2) = %q, want mid.ibt", filepath.Base(got))
	}
}

func TestNthLatestIbtFile_IndexOutOfRange(t *testing.T) {
	dir := t.TempDir()
	createIbtFiles(t, dir, []string{"only.ibt"})

	_, err := nthLatestIbtFile(dir, 2)
	if err == nil {
		t.Error("nthLatestIbtFile(2) with 1 file: expected error, got nil")
	}
}

func TestNthLatestIbtFile_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := nthLatestIbtFile(dir, 1)
	if err == nil {
		t.Error("nthLatestIbtFile on empty dir: expected error, got nil")
	}
}

// TestNthLatestIbtFile_UppercaseExtension guards the case-insensitive match:
// Windows filesystems are case-insensitive, so a file written as .IBT must not
// be skipped and reported as "no .ibt files found".
func TestNthLatestIbtFile_UppercaseExtension(t *testing.T) {
	dir := t.TempDir()
	createIbtFiles(t, dir, []string{"SESSION.IBT"})

	got, err := nthLatestIbtFile(dir, 1)
	if err != nil {
		t.Fatalf("nthLatestIbtFile: %v", err)
	}
	if filepath.Base(got) != "SESSION.IBT" {
		t.Errorf("nthLatestIbtFile = %q, want SESSION.IBT", filepath.Base(got))
	}
}

// TestDumpSegmentPath_UsesDir verifies -dump output is placed next to the .ibt
// rather than resolving against an unpredictable working directory.
func TestDumpSegmentPath_UsesDir(t *testing.T) {
	got := dumpSegmentPath(filepath.Join("C:", "telemetry"), "T3", 4)
	want := filepath.Join("C:", "telemetry", "T3_lap4.csv")
	if got != want {
		t.Errorf("dumpSegmentPath = %q, want %q", got, want)
	}
}

func TestDumpSegmentPath_EmptyDirIsBareName(t *testing.T) {
	got := dumpSegmentPath("", "T3", 4)
	if got != "T3_lap4.csv" {
		t.Errorf("dumpSegmentPath = %q, want T3_lap4.csv", got)
	}
}

func TestNthLatestIbtFile_IgnoresNonIbt(t *testing.T) {
	dir := t.TempDir()
	// Create a .txt file — should be ignored.
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0644)
	createIbtFiles(t, dir, []string{"session.ibt"})

	got, err := nthLatestIbtFile(dir, 1)
	if err != nil {
		t.Fatalf("nthLatestIbtFile: %v", err)
	}
	if filepath.Base(got) != "session.ibt" {
		t.Errorf("nthLatestIbtFile = %q, want session.ibt", filepath.Base(got))
	}
}

