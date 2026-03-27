package analysis

import (
	"math"
	"testing"

	"github.com/rickymw/SimAppLauncher/internal/trackmap"
)

// ---- FormatLapTime ----

func TestFormatLapTime(t *testing.T) {
	cases := []struct {
		secs float32
		want string
	}{
		{0, "?:??.???"},
		{-1, "?:??.???"},
		{60.0, "1:00.000"},
		{95.617, "1:35.617"},
		{155.617, "2:35.617"},
		{3723.456, "62:03.456"},
	}
	for _, tc := range cases {
		got := FormatLapTime(tc.secs)
		if got != tc.want {
			t.Errorf("FormatLapTime(%v) = %q, want %q", tc.secs, got, tc.want)
		}
	}
}

// ---- yamlField ----

func TestYamlField(t *testing.T) {
	yaml := `
WeekendInfo:
 TrackDisplayName: Sebring International Raceway
 TrackName: sebring international
DriverInfo:
 DriverCarIdx: 4
`
	cases := []struct{ key, want string }{
		{"TrackDisplayName", "Sebring International Raceway"},
		{"TrackName", "sebring international"},
		{"DriverCarIdx", "4"},
		{"Missing", ""},
	}
	for _, tc := range cases {
		if got := yamlField(yaml, tc.key); got != tc.want {
			t.Errorf("yamlField(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// ---- driverBlockByIdx ----

const multiDriverYAML = `
DriverInfo:
 DriverCarIdx: 1
 Drivers:
 - CarIdx: 0
   UserName: Other Driver
   CarScreenName: Ligier JS P320
 - CarIdx: 1
   UserName: Ricky Maw
   CarScreenName: Porsche 718 Cayman GT4
 - CarIdx: 2
   UserName: Third Driver
   CarScreenName: BMW M4 GT3
`

func TestDriverBlockByIdx(t *testing.T) {
	t.Run("first driver", func(t *testing.T) {
		u, c := driverBlockByIdx(multiDriverYAML, 0)
		if u != "Other Driver" || c != "Ligier JS P320" {
			t.Errorf("got (%q, %q)", u, c)
		}
	})
	t.Run("middle driver", func(t *testing.T) {
		u, c := driverBlockByIdx(multiDriverYAML, 1)
		if u != "Ricky Maw" || c != "Porsche 718 Cayman GT4" {
			t.Errorf("got (%q, %q)", u, c)
		}
	})
	t.Run("last driver", func(t *testing.T) {
		u, c := driverBlockByIdx(multiDriverYAML, 2)
		if u != "Third Driver" || c != "BMW M4 GT3" {
			t.Errorf("got (%q, %q)", u, c)
		}
	})
	t.Run("no match", func(t *testing.T) {
		u, c := driverBlockByIdx(multiDriverYAML, 99)
		if u != "" || c != "" {
			t.Errorf("expected empty, got (%q, %q)", u, c)
		}
	})
}

// ---- driverBlockByName ----

func TestDriverBlockByName(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		u, c := driverBlockByName(multiDriverYAML, "Ricky Maw")
		if u != "Ricky Maw" || c != "Porsche 718 Cayman GT4" {
			t.Errorf("got (%q, %q)", u, c)
		}
	})
	t.Run("case insensitive", func(t *testing.T) {
		u, c := driverBlockByName(multiDriverYAML, "ricky maw")
		if u != "Ricky Maw" || c != "Porsche 718 Cayman GT4" {
			t.Errorf("got (%q, %q)", u, c)
		}
	})
	t.Run("last driver", func(t *testing.T) {
		u, c := driverBlockByName(multiDriverYAML, "Third Driver")
		if u != "Third Driver" || c != "BMW M4 GT3" {
			t.Errorf("got (%q, %q)", u, c)
		}
	})
	t.Run("no match", func(t *testing.T) {
		u, c := driverBlockByName(multiDriverYAML, "Nobody")
		if u != "" || c != "" {
			t.Errorf("expected empty, got (%q, %q)", u, c)
		}
	})
}

// ---- ParseSessionMeta ----

func TestParseSessionMeta(t *testing.T) {
	t.Run("match by driver name", func(t *testing.T) {
		m := ParseSessionMeta(multiDriverYAML, "Ricky Maw")
		if m.CarScreenName != "Porsche 718 Cayman GT4" {
			t.Errorf("CarScreenName = %q", m.CarScreenName)
		}
		if m.DriverName != "Ricky Maw" {
			t.Errorf("DriverName = %q", m.DriverName)
		}
	})
	t.Run("fall back to DriverCarIdx when name unknown", func(t *testing.T) {
		m := ParseSessionMeta(multiDriverYAML, "Nobody")
		// DriverCarIdx = 1 → Ricky Maw / Porsche
		if m.CarScreenName != "Porsche 718 Cayman GT4" {
			t.Errorf("CarScreenName = %q", m.CarScreenName)
		}
	})
	t.Run("fall back to DriverCarIdx when no driver supplied", func(t *testing.T) {
		m := ParseSessionMeta(multiDriverYAML, "")
		if m.CarScreenName != "Porsche 718 Cayman GT4" {
			t.Errorf("CarScreenName = %q", m.CarScreenName)
		}
	})
	t.Run("track name parsed", func(t *testing.T) {
		yaml := "WeekendInfo:\n TrackDisplayName: Spa-Francorchamps\n"
		m := ParseSessionMeta(yaml, "")
		if m.TrackDisplayName != "Spa-Francorchamps" {
			t.Errorf("TrackDisplayName = %q", m.TrackDisplayName)
		}
	})
}

// ---- finalizeLap (LapKind detection) ----

func makeLap(firstSpeed, lastSpeed float32, n int) *Lap {
	samples := make([]SampleData, n)
	for i := range samples {
		samples[i].Speed = firstSpeed
		samples[i].SessionTime = float64(i)
		samples[i].LapDistPct = float32(i) / float32(n-1)
	}
	samples[n-1].Speed = lastSpeed
	lap := &Lap{Samples: samples}
	finalizeLap(lap)
	return lap
}

func TestFinalizeLap_Kinds(t *testing.T) {
	cases := []struct {
		name                string
		firstSpd, lastSpd   float32
		want                LapKind
	}{
		{"flying", 50, 50, KindFlying},
		{"out lap", 0, 50, KindOutLap},
		{"in lap", 50, 0, KindInLap},
		{"out/in lap", 0, 0, KindOutInLap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lap := makeLap(tc.firstSpd, tc.lastSpd, 10)
			if lap.Kind != tc.want {
				t.Errorf("Kind = %v, want %v", lap.Kind, tc.want)
			}
		})
	}
}

func TestFinalizeLap_LapTime(t *testing.T) {
	// 10 samples: SessionTime 0..9 → duration = 9s
	lap := makeLap(50, 50, 10)
	if lap.LapTime != 9.0 {
		t.Errorf("LapTime = %v, want 9.0", lap.LapTime)
	}
}

// ---- zoneIdx ----

func TestZoneIdx(t *testing.T) {
	cases := []struct {
		pct  float32
		want int
	}{
		{0.0, 0},
		{0.049, 0},
		{0.05, 1},
		{0.5, 10},
		{0.999, 19},
		{1.0, 19}, // clamped
		{-0.1, 0}, // clamped
	}
	for _, tc := range cases {
		if got := zoneIdx(tc.pct); got != tc.want {
			t.Errorf("zoneIdx(%v) = %d, want %d", tc.pct, got, tc.want)
		}
	}
}

// ---- ZoneStats ----

// uniformLap builds a Lap with n samples evenly distributed across the track.
// Each sample has the given speed (m/s), throttle, brake, and no ABS.
func uniformLap(n int, speed, throttle, brake float32) *Lap {
	samples := make([]SampleData, n)
	for i := range samples {
		samples[i] = SampleData{
			LapDistPct: float32(i) / float32(n),
			SessionTime: float64(i),
			Speed:      speed,
			Throttle:   throttle,
			Brake:      brake,
		}
	}
	lap := &Lap{Samples: samples}
	finalizeLap(lap)
	return lap
}

func TestZoneStats_Basic(t *testing.T) {
	// 200 samples, uniform speed of 50 m/s (180 km/h), full throttle.
	lap := uniformLap(200, 50, 1.0, 0)
	zones := ZoneStats(lap)

	if len(zones) != NumZones {
		t.Fatalf("len(zones) = %d, want %d", len(zones), NumZones)
	}
	for i, z := range zones {
		if z.SampleCount == 0 {
			t.Errorf("zone %d: SampleCount = 0", i)
		}
		wantSpd := float32(50 * ms2kmh) // 180 km/h
		if math.Abs(float64(z.SpeedMinKPH-wantSpd)) > 1 {
			t.Errorf("zone %d: SpeedMinKPH = %.1f, want ~%.1f", i, z.SpeedMinKPH, wantSpd)
		}
		if math.Abs(float64(z.ThrottlePct-100)) > 0.1 {
			t.Errorf("zone %d: ThrottlePct = %.1f, want 100", i, z.ThrottlePct)
		}
		if z.BrakePct != 0 {
			t.Errorf("zone %d: BrakePct = %.1f, want 0", i, z.BrakePct)
		}
	}
}

func TestZoneStats_ABSAndCoast(t *testing.T) {
	// Build a lap where every sample in zone 0 (dist 0–5%) has ABS active
	// and every sample in zone 1 (dist 5–10%) is coasting.
	samples := make([]SampleData, 40)
	for i := range samples {
		pct := float32(i) / float32(len(samples))
		samples[i] = SampleData{
			LapDistPct: pct,
			Speed:      30,
			Throttle:   1.0,
			Brake:      0,
		}
		if pct < 0.05 {
			samples[i].ABSActive = true
		}
		if pct >= 0.05 && pct < 0.10 {
			samples[i].Throttle = 0
			samples[i].Brake = 0
		}
	}
	lap := &Lap{Samples: samples}
	finalizeLap(lap)
	zones := ZoneStats(lap)

	if zones[0].ABSCount == 0 {
		t.Error("zone 0: expected ABS activations, got 0")
	}
	if zones[0].CoastSamples != 0 {
		t.Errorf("zone 0: CoastSamples = %d, want 0", zones[0].CoastSamples)
	}
	if zones[1].CoastSamples == 0 {
		t.Error("zone 1: expected coast samples, got 0")
	}
	if zones[1].ABSCount != 0 {
		t.Errorf("zone 1: ABSCount = %d, want 0", zones[1].ABSCount)
	}
}

func TestZoneStats_DominantGear(t *testing.T) {
	// 100 samples across the lap; zone 0 (pct 0.00–0.05) has 5 samples,
	// 4 of which are gear 3 and 1 is gear 2 — gear 3 should dominate.
	samples := make([]SampleData, 100)
	for i := range samples {
		pct := float32(i) / float32(len(samples))
		gear := int32(3)
		if pct < 0.05 && i%5 == 0 {
			gear = 2 // 1 in 5 samples in zone 0 is gear 2
		}
		samples[i] = SampleData{LapDistPct: pct, Speed: 30, Gear: gear}
	}
	lap := &Lap{Samples: samples}
	finalizeLap(lap)
	zones := ZoneStats(lap)

	if zones[0].DominantGear != 3 {
		t.Errorf("zone 0 DominantGear = %d, want 3", zones[0].DominantGear)
	}
}

// ---- ParseTrackLength ----

func TestParseTrackLength_KM(t *testing.T) {
	yaml := "WeekendInfo:\n TrackLength: 6.02 km\n"
	got := ParseTrackLength(yaml)
	if math.Abs(got-6020.0) > 0.1 {
		t.Errorf("ParseTrackLength = %v, want 6020.0", got)
	}
}

func TestParseTrackLength_Missing(t *testing.T) {
	got := ParseTrackLength("")
	if got != 0 {
		t.Errorf("ParseTrackLength = %v, want 0", got)
	}
}

func TestParseTrackLength_Malformed(t *testing.T) {
	yaml := "WeekendInfo:\n TrackLength: unknown\n"
	got := ParseTrackLength(yaml)
	if got != 0 {
		t.Errorf("ParseTrackLength = %v, want 0", got)
	}
}

// ---- ZoneDeltas ----

// timedLap builds a lap where LapDistPct goes linearly from 0 to 1 and
// SessionTime goes from 0 to lapDuration (in seconds).
func timedLap(n int, lapDuration float64) *Lap {
	samples := make([]SampleData, n)
	for i := range samples {
		frac := float64(i) / float64(n-1)
		samples[i] = SampleData{
			LapDistPct:  float32(frac),
			SessionTime: frac * lapDuration,
			Speed:       30,
		}
	}
	return &Lap{
		StartSessionTime: 0,
		Samples:          samples,
	}
}

func TestZoneDeltas_IdenticalLaps(t *testing.T) {
	lap := timedLap(200, 100.0)
	deltas := ZoneDeltas(lap, lap)
	for i, d := range deltas {
		if math.Abs(float64(d)) > 0.01 {
			t.Errorf("zone %d: delta = %v, want ~0", i, d)
		}
	}
}

func TestZoneDeltas_UniformSpeedup(t *testing.T) {
	// lap1 = 100s, lap2 = 90s → each zone should show lap2 0.5s faster (delta = −0.5)
	lap1 := timedLap(500, 100.0)
	lap2 := timedLap(500, 90.0)
	deltas := ZoneDeltas(lap1, lap2)

	for i, d := range deltas {
		const wantDelta = float32(-0.5)
		if math.Abs(float64(d-wantDelta)) > 0.05 {
			t.Errorf("zone %d: delta = %+.3f, want ~%.3f", i, d, wantDelta)
		}
	}

	// Sum should equal overall difference: 90 − 100 = −10s
	var sum float32
	for _, d := range deltas {
		sum += d
	}
	if math.Abs(float64(sum+10)) > 0.1 {
		t.Errorf("sum of deltas = %+.3f, want ~−10.000", sum)
	}
}

// ---- SegmentStats ----

// segLap builds a lap with samples spanning two explicit segments.
// Segment 0 covers pct [0, 0.5) with speed=20 m/s and ABS active.
// Segment 1 covers pct [0.5, 1.0) with speed=40 m/s and no ABS.
func segLap() *Lap {
	n := 200
	samples := make([]SampleData, n)
	for i := range samples {
		pct := float32(i) / float32(n)
		speed := float32(20.0)
		abs := true
		if pct >= 0.5 {
			speed = 40.0
			abs = false
		}
		samples[i] = SampleData{
			LapDistPct:  pct,
			SessionTime: float64(i) * 0.5,
			Speed:       speed,
			Throttle:    1.0,
			ABSActive:   abs,
		}
	}
	lap := &Lap{Samples: samples}
	finalizeLap(lap)
	return lap
}

func TestSegmentStats_Basic(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}
	lap := segLap()
	zones := SegmentStats(lap, segs)

	if len(zones) != 2 {
		t.Fatalf("len(zones) = %d, want 2", len(zones))
	}

	// Segment 0: speed 20 m/s → 72 km/h
	wantSpd0 := float32(20.0 * ms2kmh)
	if math.Abs(float64(zones[0].SpeedMinKPH-wantSpd0)) > 1.0 {
		t.Errorf("zones[0].SpeedMinKPH = %.1f, want %.1f", zones[0].SpeedMinKPH, wantSpd0)
	}
	if zones[0].SpeedEntryKPH != wantSpd0 {
		t.Errorf("zones[0].SpeedEntryKPH = %.1f, want %.1f", zones[0].SpeedEntryKPH, wantSpd0)
	}
	if zones[0].ABSCount == 0 {
		t.Error("zones[0]: expected ABSCount > 0")
	}

	// Segment 1: speed 40 m/s → 144 km/h, no ABS
	wantSpd1 := float32(40.0 * ms2kmh)
	if math.Abs(float64(zones[1].SpeedMinKPH-wantSpd1)) > 1.0 {
		t.Errorf("zones[1].SpeedMinKPH = %.1f, want %.1f", zones[1].SpeedMinKPH, wantSpd1)
	}
	if zones[1].ABSCount != 0 {
		t.Errorf("zones[1].ABSCount = %d, want 0", zones[1].ABSCount)
	}
}

// ---- SegmentDeltas ----

func TestSegmentDeltas_IdenticalLaps(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}
	lap := timedLap(200, 100.0)
	deltas := SegmentDeltas(lap, lap, segs)
	for i, d := range deltas {
		if math.Abs(float64(d)) > 0.01 {
			t.Errorf("segment %d: delta = %v, want ~0", i, d)
		}
	}
}
