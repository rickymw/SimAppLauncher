package analysis

import (
	"math"
	"testing"
)

// realYAML mirrors the SplitTimeInfo block iRacing writes, taken verbatim from
// a Road America .ibt.
const realYAML = `WeekendInfo:
 TrackName: roadamerica full
SplitTimeInfo:
 Sectors:
 - SectorNum: 0
   SectorStartPct: 0.000000
 - SectorNum: 1
   SectorStartPct: 0.143086
 - SectorNum: 2
   SectorStartPct: 0.326681
 - SectorNum: 3
   SectorStartPct: 0.478771
 - SectorNum: 4
   SectorStartPct: 0.760854
 - SectorNum: 5
   SectorStartPct: 0.865939

CarSetup:
 UpdateCount: 13
`

func TestParseSectors_RealYAML(t *testing.T) {
	got := ParseSectors(realYAML)
	if len(got) != 6 {
		t.Fatalf("ParseSectors returned %d sectors, want 6", len(got))
	}
	wantPct := []float32{0, 0.143086, 0.326681, 0.478771, 0.760854, 0.865939}
	for i, w := range wantPct {
		if got[i].Num != i {
			t.Errorf("sector %d: Num = %d, want %d", i, got[i].Num, i)
		}
		if math.Abs(float64(got[i].StartPct-w)) > 1e-6 {
			t.Errorf("sector %d: StartPct = %v, want %v", i, got[i].StartPct, w)
		}
	}
}

// TestParseSectors_StopsAtNextTopLevelKey guards against swallowing the
// CarSetup block that follows SplitTimeInfo.
func TestParseSectors_StopsAtNextTopLevelKey(t *testing.T) {
	if got := ParseSectors(realYAML); len(got) != 6 {
		t.Errorf("ParseSectors read past the block: got %d sectors, want 6", len(got))
	}
}

func TestParseSectors_AtStartOfDocument(t *testing.T) {
	yaml := "SplitTimeInfo:\n Sectors:\n - SectorNum: 0\n   SectorStartPct: 0.000000\n"
	if got := ParseSectors(yaml); len(got) != 1 {
		t.Errorf("ParseSectors at document start: got %d sectors, want 1", len(got))
	}
}

func TestParseSectors_Missing(t *testing.T) {
	if got := ParseSectors("WeekendInfo:\n TrackName: x\n"); got != nil {
		t.Errorf("ParseSectors with no SplitTimeInfo = %v, want nil", got)
	}
}

// sectorLap builds a lap whose LapDistPct rises linearly from 0 to ~1 at a
// steady 1 pct-unit per second, so a sector spanning 0.25 of the lap should
// measure 0.25 * total.
func sectorLap(totalSecs float32, n int) *Lap {
	s := make([]SampleData, n)
	for i := range s {
		f := float32(i) / float32(n-1)
		s[i].LapDistPct = f * 0.999
		s[i].SessionTime = 100 + float64(f*totalSecs)
	}
	return &Lap{Number: 1, Samples: s, LapTime: totalSecs}
}

func TestComputeSectorTimes_EvenSplit(t *testing.T) {
	lap := sectorLap(100, 1001)
	sectors := []Sector{{0, 0.0}, {1, 0.25}, {2, 0.5}, {3, 0.75}}

	got := ComputeSectorTimes(lap, sectors)
	if len(got) != 4 {
		t.Fatalf("got %d sector times, want 4", len(got))
	}
	for i, st := range got {
		if !st.Complete {
			t.Errorf("sector %d: not complete", i)
			continue
		}
		if math.Abs(float64(st.Seconds-25)) > 0.5 {
			t.Errorf("sector %d: Seconds = %.3f, want ~25", i, st.Seconds)
		}
	}
}

// TestComputeSectorTimes_SumsToLap verifies the sector times account for the
// whole lap - the property that makes them trustworthy for coaching.
func TestComputeSectorTimes_SumsToLap(t *testing.T) {
	lap := sectorLap(128.406, 2000)
	sectors := ParseSectors(realYAML)

	got := ComputeSectorTimes(lap, sectors)
	var sum float32
	for _, st := range got {
		if !st.Complete {
			t.Fatalf("sector %d incomplete on a full lap", st.Num)
		}
		sum += st.Seconds
	}
	if math.Abs(float64(sum-128.406)) > 0.2 {
		t.Errorf("sector times sum to %.3f, want ~128.406", sum)
	}
}

func TestComputeSectorTimes_BoundariesAreInterpolated(t *testing.T) {
	// 11 samples over 100s: boundaries land between samples, so snapping to the
	// nearest sample would be visibly wrong.
	lap := sectorLap(100, 11)
	got := ComputeSectorTimes(lap, []Sector{{0, 0.0}, {1, 0.15}})
	if !got[0].Complete {
		t.Fatal("sector 0 incomplete")
	}
	// 0.15 of 0.999 total travel -> ~15.0s in.
	if math.Abs(float64(got[0].Seconds-15.0)) > 1.5 {
		t.Errorf("sector 0 = %.3f, want ~15.0 (interpolated, not snapped)", got[0].Seconds)
	}
}

func TestComputeSectorTimes_NilAndEmpty(t *testing.T) {
	if got := ComputeSectorTimes(nil, []Sector{{0, 0}}); got != nil {
		t.Errorf("nil lap: got %v, want nil", got)
	}
	if got := ComputeSectorTimes(sectorLap(60, 100), nil); got != nil {
		t.Errorf("no sectors: got %v, want nil", got)
	}
}

// TestComputeSectorTimes_PartialLap covers a recording that stops mid-lap: the
// sectors never reached must report Complete false rather than a bogus time.
func TestComputeSectorTimes_PartialLap(t *testing.T) {
	s := make([]SampleData, 200)
	for i := range s {
		f := float32(i) / 199
		s[i].LapDistPct = f * 0.40 // only the first 40% of the lap
		s[i].SessionTime = float64(f * 50)
	}
	lap := &Lap{Number: 2, Samples: s}

	got := ComputeSectorTimes(lap, []Sector{{0, 0.0}, {1, 0.25}, {2, 0.5}, {3, 0.75}})
	if !got[0].Complete {
		t.Error("sector 0 should be complete on a lap covering 0-40%")
	}
	if got[3].Complete {
		t.Error("sector 3 must not be complete when the lap ends at 40%")
	}
}

func TestBestSectorTimes(t *testing.T) {
	perLap := [][]SectorTime{
		{{Num: 0, Seconds: 30, Complete: true}, {Num: 1, Seconds: 45, Complete: true}},
		{{Num: 0, Seconds: 28, Complete: true}, {Num: 1, Seconds: 47, Complete: true}},
		{{Num: 0, Seconds: 31, Complete: true}, {Num: 1, Seconds: 44, Complete: true}},
	}
	best, from := BestSectorTimes(perLap, []int{5, 6, 7})

	if len(best) != 2 {
		t.Fatalf("got %d best sectors, want 2", len(best))
	}
	if best[0].Seconds != 28 || from[0] != 6 {
		t.Errorf("sector 0 best = %.1f from lap %d, want 28.0 from lap 6", best[0].Seconds, from[0])
	}
	if best[1].Seconds != 44 || from[1] != 7 {
		t.Errorf("sector 1 best = %.1f from lap %d, want 44.0 from lap 7", best[1].Seconds, from[1])
	}
}

func TestBestSectorTimes_IgnoresIncomplete(t *testing.T) {
	perLap := [][]SectorTime{
		{{Num: 0, Seconds: 10, Complete: false}}, // faster but incomplete
		{{Num: 0, Seconds: 30, Complete: true}},
	}
	best, from := BestSectorTimes(perLap, []int{1, 2})
	if best[0].Seconds != 30 || from[0] != 2 {
		t.Errorf("best = %.1f from lap %d, want 30.0 from lap 2 (incomplete must be ignored)", best[0].Seconds, from[0])
	}
}

// ---- TrackNumTurns ----

func TestParseTrackNumTurns(t *testing.T) {
	yaml := "WeekendInfo:\n TrackName: roadamerica full\n TrackNumTurns: 14\n"
	if got := ParseTrackNumTurns(yaml); got != 14 {
		t.Errorf("ParseTrackNumTurns = %d, want 14", got)
	}
}

func TestParseTrackNumTurns_Missing(t *testing.T) {
	if got := ParseTrackNumTurns("WeekendInfo:\n TrackName: x\n"); got != 0 {
		t.Errorf("ParseTrackNumTurns with no field = %d, want 0", got)
	}
}

func TestParseTrackNumTurns_Malformed(t *testing.T) {
	for _, y := range []string{
		"TrackNumTurns: \n",
		"TrackNumTurns: abc\n",
		"TrackNumTurns: -3\n",
	} {
		if got := ParseTrackNumTurns(y); got != 0 {
			t.Errorf("ParseTrackNumTurns(%q) = %d, want 0", y, got)
		}
	}
}
