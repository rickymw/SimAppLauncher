package analysis

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/rickymw/SimAppLauncher/internal/ibt"
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

// ---- LapKind.String ----

func TestLapKind_String(t *testing.T) {
	cases := []struct {
		kind LapKind
		want string
	}{
		{KindFlying, "flying lap"},
		{KindOutLap, "out lap"},
		{KindInLap, "in lap"},
		{KindOutInLap, "out/in lap"},
		{LapKind(99), "flying lap"}, // default branch
	}
	for _, tc := range cases {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("LapKind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

// ---- ExtractLaps ----
//
// The helpers below construct a minimal valid .ibt binary so that
// ExtractLaps can be tested without a real telemetry recording.

// lbRawVarBuf mirrors ibt.rawVarBuf (16 bytes).
type lbRawVarBuf struct {
	TickCount int32
	BufOffset int32
	Pad       [2]int32
}

// lbRawHeader mirrors ibt.rawHeader (112 bytes at offset 0).
type lbRawHeader struct {
	Ver               int32
	Status            int32
	TickRate          int32
	SessionInfoUpdate int32
	SessionInfoLen    int32
	SessionInfoOffset int32
	NumVars           int32
	VarHeaderOffset   int32
	NumBuf            int32
	BufLen            int32
	Pad               [2]int32
	VarBuf            [4]lbRawVarBuf
}

// lbRawDiskHeader mirrors ibt.rawDiskHeader (32 bytes at offset 112).
type lbRawDiskHeader struct {
	SessionStartDate   int64
	SessionStartTime   float64
	SessionEndTime     float64
	SessionLapCount    int32
	SessionRecordCount int32
}

// lbRawVarHeader mirrors ibt.rawVarHeader (144 bytes).
type lbRawVarHeader struct {
	Type        int32
	Offset      int32
	Count       int32
	CountAsTime bool
	Pad         [3]byte
	Name        [32]byte
	Desc        [64]byte
	Unit        [32]byte
}

// lbSample is one row of data for the lap builder file.
type lbSample struct {
	LapDistPct  float32
	Speed       float32
	SessionTime float64
}

func lbPad32(s string) [32]byte { var b [32]byte; copy(b[:], s); return b }
func lbPad64(s string) [64]byte { var b [64]byte; copy(b[:], s); return b }

const (
	lbSessionInfoOffset = 144
	lbSessionInfoLen    = 64
	lbVarHeaderOffset   = 208
	lbNumVars           = 3
	lbBufLen            = 16 // LapDistPct(4) + Speed(4) + SessionTime(8)
	lbDataOffset        = lbVarHeaderOffset + lbNumVars*144 // 208 + 432 = 640

	// VarType constants (mirroring ibt package values)
	lbVarTypeFloat  int32 = 4
	lbVarTypeDouble int32 = 5
)

// buildLapIBTFile writes a minimal .ibt temp file with LapDistPct, Speed,
// and SessionTime channels, then returns its path.
func buildLapIBTFile(t *testing.T, samples []lbSample) string {
	t.Helper()

	var buf bytes.Buffer

	hdr := lbRawHeader{
		Ver: 1, Status: 1, TickRate: 60,
		SessionInfoLen: lbSessionInfoLen, SessionInfoOffset: lbSessionInfoOffset,
		NumVars: lbNumVars, VarHeaderOffset: lbVarHeaderOffset,
		NumBuf: 1, BufLen: lbBufLen,
	}
	hdr.VarBuf[0] = lbRawVarBuf{TickCount: 0, BufOffset: lbDataOffset}
	if err := binary.Write(&buf, binary.LittleEndian, hdr); err != nil {
		t.Fatalf("writing header: %v", err)
	}

	disk := lbRawDiskHeader{
		SessionStartDate: 1_700_000_000, SessionStartTime: 3600, SessionEndTime: 3700,
		SessionLapCount: 3, SessionRecordCount: int32(len(samples)),
	}
	if err := binary.Write(&buf, binary.LittleEndian, disk); err != nil {
		t.Fatalf("writing disk header: %v", err)
	}

	si := make([]byte, lbSessionInfoLen)
	copy(si, "---\nSessionNum: 0\n")
	buf.Write(si)

	varHeaders := []lbRawVarHeader{
		{Type: lbVarTypeFloat, Offset: 0, Count: 1, Name: lbPad32("LapDistPct"), Desc: lbPad64("Lap pct"), Unit: lbPad32("")},
		{Type: lbVarTypeFloat, Offset: 4, Count: 1, Name: lbPad32("Speed"), Desc: lbPad64("Speed"), Unit: lbPad32("m/s")},
		{Type: lbVarTypeDouble, Offset: 8, Count: 1, Name: lbPad32("SessionTime"), Desc: lbPad64("Session time"), Unit: lbPad32("s")},
	}
	for _, vh := range varHeaders {
		if err := binary.Write(&buf, binary.LittleEndian, vh); err != nil {
			t.Fatalf("writing var header: %v", err)
		}
	}

	for _, s := range samples {
		row := make([]byte, lbBufLen)
		binary.LittleEndian.PutUint32(row[0:], math.Float32bits(s.LapDistPct))
		binary.LittleEndian.PutUint32(row[4:], math.Float32bits(s.Speed))
		binary.LittleEndian.PutUint64(row[8:], math.Float64bits(s.SessionTime))
		buf.Write(row)
	}

	tmp, err := os.CreateTemp("", "test-lap-*.ibt")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	path := tmp.Name()
	t.Cleanup(func() { os.Remove(path) })
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		t.Fatalf("writing ibt: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	return path
}

// makeLapSamples builds a linear sample sequence from pctStart to pctEnd
// with the given speed. SessionTime increments by 1/60 per sample.
func makeLapSamples(n int, pctStart, pctEnd, speed float32, timeOffset float64) []lbSample {
	out := make([]lbSample, n)
	for i := range out {
		frac := float32(0)
		if n > 1 {
			frac = float32(i) / float32(n-1)
		}
		out[i] = lbSample{
			LapDistPct:  pctStart + frac*(pctEnd-pctStart),
			Speed:       speed,
			SessionTime: timeOffset + float64(i)/60.0,
		}
	}
	return out
}

func TestExtractLaps_Empty(t *testing.T) {
	path := buildLapIBTFile(t, nil)
	f, err := ibt.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	laps, err := ExtractLaps(f)
	if err != nil {
		t.Fatalf("ExtractLaps: %v", err)
	}
	if len(laps) != 0 {
		t.Errorf("expected 0 laps, got %d", len(laps))
	}
}

func TestExtractLaps_SingleCrossing(t *testing.T) {
	// Lap 1: 320 samples, LapDistPct 0.01→0.99.
	// Then S/F crossing (next sample starts at 0.02).
	// Lap 2: 50 samples (final lap, always appended).
	const lap1N, lap2N = 320, 50

	lap1 := makeLapSamples(lap1N, 0.01, 0.99, 30, 0)
	lap2 := makeLapSamples(lap2N, 0.02, 0.30, 30, float64(lap1N)/60.0)
	samples := append(lap1, lap2...)

	path := buildLapIBTFile(t, samples)
	f, err := ibt.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	laps, err := ExtractLaps(f)
	if err != nil {
		t.Fatalf("ExtractLaps: %v", err)
	}
	if len(laps) != 2 {
		t.Fatalf("expected 2 laps, got %d", len(laps))
	}

	// Lap 1 checks.
	if laps[0].IsPartialStart {
		t.Error("lap 1: expected IsPartialStart=false")
	}
	if len(laps[0].Samples) != lap1N {
		t.Errorf("lap 1: %d samples, want %d", len(laps[0].Samples), lap1N)
	}
	if laps[0].Kind != KindFlying {
		t.Errorf("lap 1: Kind=%v, want KindFlying", laps[0].Kind)
	}
	wantLapTime1 := float32(float64(lap1N-1) / 60.0)
	if math.Abs(float64(laps[0].LapTime-wantLapTime1)) > 0.01 {
		t.Errorf("lap 1: LapTime=%.3f, want ~%.3f", laps[0].LapTime, wantLapTime1)
	}

	// Lap 2 checks.
	if len(laps[1].Samples) != lap2N {
		t.Errorf("lap 2: %d samples, want %d", len(laps[1].Samples), lap2N)
	}
}

func TestExtractLaps_TooShortFirstLap(t *testing.T) {
	// 10-sample "lap" before S/F crossing — too short to keep (< MinSamplesForValidLap).
	// 30-sample final lap — always appended.
	lap1 := makeLapSamples(10, 0.60, 0.90, 30, 0)
	lap2 := makeLapSamples(30, 0.02, 0.30, 30, float64(10)/60.0)
	samples := append(lap1, lap2...)

	path := buildLapIBTFile(t, samples)
	f, err := ibt.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	laps, err := ExtractLaps(f)
	if err != nil {
		t.Fatalf("ExtractLaps: %v", err)
	}
	if len(laps) != 1 {
		t.Fatalf("expected 1 lap (short first lap dropped), got %d", len(laps))
	}
	if len(laps[0].Samples) != 30 {
		t.Errorf("expected 30 samples in final lap, got %d", len(laps[0].Samples))
	}
}

func TestExtractLaps_IsPartialStart(t *testing.T) {
	// First sample at 0.10 > 0.05 → IsPartialStart = true.
	samples := makeLapSamples(320, 0.10, 0.99, 30, 0)

	path := buildLapIBTFile(t, samples)
	f, err := ibt.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	laps, err := ExtractLaps(f)
	if err != nil {
		t.Fatalf("ExtractLaps: %v", err)
	}
	if len(laps) != 1 {
		t.Fatalf("expected 1 lap, got %d", len(laps))
	}
	if !laps[0].IsPartialStart {
		t.Error("expected IsPartialStart=true for lap starting at pct 0.10")
	}
}

func TestExtractLaps_ZeroArtifact(t *testing.T) {
	// iRacing emits a single LapDistPct=0.0 frame at the S/F crossing.
	// That artifact sample must be absorbed: not added to either lap.
	const lap1N = 320
	lap1 := makeLapSamples(lap1N, 0.01, 0.99, 30, 0)

	// Artifact sample: LapDistPct exactly 0.0, prevDist (0.99) > 0.5.
	artifact := lbSample{LapDistPct: 0.0, Speed: 30, SessionTime: float64(lap1N) / 60.0}

	// Lap 2: 50 samples.
	lap2 := makeLapSamples(50, 0.01, 0.30, 30, float64(lap1N+1)/60.0)

	samples := append(lap1, artifact)
	samples = append(samples, lap2...)

	path := buildLapIBTFile(t, samples)
	f, err := ibt.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	laps, err := ExtractLaps(f)
	if err != nil {
		t.Fatalf("ExtractLaps: %v", err)
	}
	if len(laps) != 2 {
		t.Fatalf("expected 2 laps, got %d", len(laps))
	}
	// Artifact sample must not appear in either lap.
	if len(laps[0].Samples) != lap1N {
		t.Errorf("lap 1: %d samples, want %d (artifact must be excluded)", len(laps[0].Samples), lap1N)
	}
	if len(laps[1].Samples) != 50 {
		t.Errorf("lap 2: %d samples, want 50", len(laps[1].Samples))
	}
}
