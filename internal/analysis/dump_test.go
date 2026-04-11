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
		{"t1", 1},   // case-insensitive
		{"T2", 3},
		{"S1", 0},
		{"1", 0},    // 1-based index
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
	if commentCount != 4 {
		t.Errorf("expected 4 comment lines, got %d", commentCount)
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
