package analysis

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

func TestResolveSegmentName(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight},
		{Name: "T1", Kind: trackmap.KindCorner},
		{Name: "S2", Kind: trackmap.KindStraight},
		{Name: "T2", Kind: trackmap.KindCorner},
	}

	tests := []struct {
		input string
		want  int
	}{
		{"T1", 1},
		{"t1", 1}, // case-insensitive
		{"T2", 3},
		{"S1", 0},
		{"1", 0}, // 1-based index
		{"2", 1},
		{"4", 3},
		{"0", -1},   // 0 is invalid (1-based)
		{"5", -1},   // out of range
		{"T9", -1},  // not found
		{"abc", -1}, // not a valid name or index
	}

	for _, tt := range tests {
		got := ResolveSegmentName(segs, tt.input)
		if got != tt.want {
			t.Errorf("ResolveSegmentName(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestDumpSegmentCSV(t *testing.T) {
	// Build a small lap with samples across two segments.
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}

	var samples []SampleData
	for i := 0; i < 120; i++ {
		pct := float32(i) / 120.0
		samples = append(samples, SampleData{
			LapDistPct:    pct,
			SessionTime:   float64(i) / 60.0,
			Speed:         50.0,
			Throttle:      0.8,
			Brake:         0.0,
			SteeringAngle: 0.1,
			Gear:          3,
			LatAccel:      5.0,
			LongAccel:     1.0,
		})
	}

	lap := &Lap{
		Number:  1,
		LapTime: 2.0,
		Samples: samples,
	}

	var buf bytes.Buffer
	cfg := DumpConfig{DownsampleRate: 3, ContextSamples: 6}
	err := DumpSegmentCSV(&buf, lap, segs, 1, cfg)
	if err != nil {
		t.Fatalf("DumpSegmentCSV error: %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Should have comment lines, header, and data rows.
	commentCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			commentCount++
		}
	}
	// Four metadata lines plus the downsampling disclosure, which only appears
	// when DownsampleRate > 1 (it is 3 here).
	if commentCount != 5 {
		t.Errorf("expected 5 comment lines, got %d", commentCount)
	}

	// Header line (first non-comment).
	headerIdx := commentCount
	if !strings.HasPrefix(lines[headerIdx], "Dist%,") {
		t.Errorf("expected header starting with 'Dist%%,', got %q", lines[headerIdx])
	}

	// Should have data rows.
	dataLines := lines[headerIdx+1:]
	if len(dataLines) == 0 {
		t.Fatal("no data rows in CSV output")
	}

	// Each data line should have 11 comma-separated fields.
	for i, line := range dataLines {
		fields := strings.Split(line, ",")
		if len(fields) != 11 {
			t.Errorf("row %d: expected 11 fields, got %d: %q", i, len(fields), line)
		}
	}
}

func TestDumpSegmentCSV_InvalidIndex(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 1.0},
	}
	lap := &Lap{Number: 1, Samples: []SampleData{{LapDistPct: 0.5}}}

	var buf bytes.Buffer
	err := DumpSegmentCSV(&buf, lap, segs, 5, DefaultDumpConfig())
	if err == nil {
		t.Error("expected error for out-of-range segment index")
	}
}

// dumpTestLap builds a lap spanning both segments of dumpTestSegs.
func dumpTestLap(number int, speed float32) Lap {
	var samples []SampleData
	const n = 120
	for i := 0; i < n; i++ {
		samples = append(samples, SampleData{
			LapDistPct:  float32(i) / float32(n),
			SessionTime: float64(number*1000) + float64(i)/60.0,
			Speed:       speed,
			Throttle:    0.8,
			Gear:        3,
		})
	}
	return Lap{Number: number, LapTime: 2.0, Kind: KindFlying, Samples: samples}
}

func dumpTestSegs() []trackmap.Segment {
	return []trackmap.Segment{
		{Name: "S1", Kind: trackmap.KindStraight, EntryPct: 0.0, ExitPct: 0.5},
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.5, ExitPct: 1.0},
	}
}

func TestDumpSegmentAllLapsCSV(t *testing.T) {
	laps := []Lap{dumpTestLap(3, 50), dumpTestLap(5, 55)}

	var buf bytes.Buffer
	cfg := DumpConfig{DownsampleRate: 3, ContextSamples: 6}
	if err := DumpSegmentAllLapsCSV(&buf, laps, dumpTestSegs(), 1, cfg); err != nil {
		t.Fatalf("DumpSegmentAllLapsCSV error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")

	var header string
	var dataLines []string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "Lap,"):
			header = line
		default:
			dataLines = append(dataLines, line)
		}
	}

	if header != "Lap,"+dumpColumns {
		t.Errorf("header = %q, want %q", header, "Lap,"+dumpColumns)
	}
	if len(dataLines) == 0 {
		t.Fatal("no data rows")
	}

	// Every row carries a lap number, and both laps are represented.
	seen := map[string]int{}
	for i, line := range dataLines {
		fields := strings.Split(line, ",")
		if len(fields) != 12 { // lap + the 11 shared columns
			t.Fatalf("row %d: expected 12 fields, got %d: %q", i, len(fields), line)
		}
		seen[fields[0]]++
	}
	if seen["3"] == 0 || seen["5"] == 0 {
		t.Errorf("expected rows for laps 3 and 5, got %v", seen)
	}
}

// Each lap's window must restart its clock at 0, otherwise the traces cannot be
// overlaid without further arithmetic.
func TestDumpSegmentAllLapsCSV_TimeRestartsPerLap(t *testing.T) {
	laps := []Lap{dumpTestLap(1, 50), dumpTestLap(2, 50)}

	var buf bytes.Buffer
	cfg := DumpConfig{DownsampleRate: 3, ContextSamples: 0}
	if err := DumpSegmentAllLapsCSV(&buf, laps, dumpTestSegs(), 1, cfg); err != nil {
		t.Fatalf("DumpSegmentAllLapsCSV error: %v", err)
	}

	firstTimeForLap := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "Lap,") {
			continue
		}
		f := strings.Split(line, ",")
		if _, ok := firstTimeForLap[f[0]]; !ok {
			firstTimeForLap[f[0]] = f[2] // Time column
		}
	}
	for lap, tm := range firstTimeForLap {
		if tm != "0.000" {
			t.Errorf("lap %s starts at Time=%s, want 0.000", lap, tm)
		}
	}
}

// A lap that never reaches the segment is skipped, not fatal — but a request
// where no lap has the segment is an error worth surfacing.
func TestDumpSegmentAllLapsCSV_SkipsLapsMissingSegment(t *testing.T) {
	full := dumpTestLap(1, 50)
	// Truncate lap 2 to the first half of the track, so it never enters T1.
	partial := dumpTestLap(2, 50)
	partial.Samples = partial.Samples[:40]

	var buf bytes.Buffer
	cfg := DumpConfig{DownsampleRate: 3, ContextSamples: 0}
	if err := DumpSegmentAllLapsCSV(&buf, []Lap{full, partial}, dumpTestSegs(), 1, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# Laps: 1 ") {
		t.Errorf("header should report only the lap that had the segment:\n%s", out)
	}

	// No lap covering the segment at all is an error.
	var buf2 bytes.Buffer
	err := DumpSegmentAllLapsCSV(&buf2, []Lap{partial}, dumpTestSegs(), 1, cfg)
	if err == nil {
		t.Error("expected an error when no lap covers the segment")
	}
}

func TestDumpSegmentAllLapsCSV_Invalid(t *testing.T) {
	segs := dumpTestSegs()
	var buf bytes.Buffer

	if err := DumpSegmentAllLapsCSV(&buf, []Lap{dumpTestLap(1, 50)}, segs, 9, DefaultDumpConfig()); err == nil {
		t.Error("expected an error for an out-of-range segment index")
	}
	if err := DumpSegmentAllLapsCSV(&buf, nil, segs, 0, DefaultDumpConfig()); err == nil {
		t.Error("expected an error for an empty lap list")
	}
}

func TestDumpSegmentCSV_FullRate(t *testing.T) {
	segs := []trackmap.Segment{
		{Name: "T1", Kind: trackmap.KindCorner, EntryPct: 0.0, ExitPct: 1.0},
	}

	var samples []SampleData
	for i := 0; i < 60; i++ {
		samples = append(samples, SampleData{
			LapDistPct:  float32(i) / 60.0,
			SessionTime: float64(i) / 60.0,
			Speed:       40.0,
			Gear:        2,
		})
	}
	lap := &Lap{Number: 1, LapTime: 1.0, Samples: samples}

	var buf bytes.Buffer
	cfg := DumpConfig{DownsampleRate: 1, ContextSamples: 0}
	err := DumpSegmentCSV(&buf, lap, segs, 0, cfg)
	if err != nil {
		t.Fatalf("DumpSegmentCSV error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	// 4 comments + 1 header + 60 data rows
	dataCount := 0
	for _, line := range lines {
		if !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "Dist%") {
			dataCount++
		}
	}
	if dataCount != 60 {
		t.Errorf("expected 60 data rows at full rate, got %d", dataCount)
	}
}
