package analysis

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

func TestDownsampleRateForHz(t *testing.T) {
	cases := []struct {
		hz   int
		want int
	}{
		{60, 1},
		{30, 2},
		{20, 3},
		{10, 6},
		{1, 60},
		// Rates that do not divide 60 round to the nearest stride rather than
		// truncating: 25Hz is closer to 30Hz (stride 2) than to 20Hz (stride 3).
		{25, 2},
		{15, 4},
		// Out of range inputs clamp rather than producing a zero or negative
		// stride, which would loop forever in writeDumpRows.
		{0, 1},
		{-5, 1},
		{120, 1},
	}
	for _, c := range cases {
		if got := DownsampleRateForHz(c.hz); got != c.want {
			t.Errorf("DownsampleRateForHz(%d) = %d, want %d", c.hz, got, c.want)
		}
	}
}

func TestDumpConfigOutputHz(t *testing.T) {
	if got := DefaultDumpConfig().OutputHz(); got != 20 {
		t.Errorf("DefaultDumpConfig OutputHz = %d, want 20", got)
	}
	if got := DefaultTraceConfig().OutputHz(); got != 60 {
		t.Errorf("DefaultTraceConfig OutputHz = %d, want 60", got)
	}
	if got := (DumpConfig{DownsampleRate: 0}).OutputHz(); got != SampleRateHz {
		t.Errorf("zero DownsampleRate OutputHz = %d, want %d", got, SampleRateHz)
	}
}

// The point of aggregating the binary channels: a lock-up lasting a couple of
// frames must survive downsampling. Plain decimation would drop it whenever it
// fell between the sampled rows, which is most of the time.
func TestWriteDumpRows_BinaryFlagsSurviveDownsampling(t *testing.T) {
	samples := make([]SampleData, 12)
	for i := range samples {
		samples[i] = SampleData{
			SessionTime: float64(i) / 60.0,
			Speed:       50,
			Throttle:    0.8,
		}
	}
	// ABS active on a single sample that decimation at stride 3 would skip
	// (rows land on 0, 3, 6, 9).
	samples[4].ABSActive = true

	var buf bytes.Buffer
	writeDumpRows(&buf, samples, DumpConfig{DownsampleRate: 3}, "")

	rows := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows at stride 3 over 12 samples, got %d", len(rows))
	}
	// Row 1 covers samples 3–5, so it must report the ABS event at sample 4.
	if got := absField(t, rows[1]); got != 1 {
		t.Errorf("row 1 ABS = %d, want 1 (event at sample 4 was dropped)", got)
	}
	// Rows covering no event must stay 0 — the aggregation must not smear.
	for _, i := range []int{0, 2, 3} {
		if got := absField(t, rows[i]); got != 0 {
			t.Errorf("row %d ABS = %d, want 0", i, got)
		}
	}
}

// Coast is derived per sample rather than read from a channel, so it needs the
// same treatment.
func TestWriteDumpRows_CoastAggregates(t *testing.T) {
	samples := make([]SampleData, 6)
	for i := range samples {
		samples[i] = SampleData{SessionTime: float64(i) / 60.0, Throttle: 0.8}
	}
	samples[1].Throttle = 0 // neither pedal: coasting, on a sample stride skips

	var buf bytes.Buffer
	writeDumpRows(&buf, samples, DumpConfig{DownsampleRate: 3}, "")

	rows := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if got := coastField(t, rows[0]); got != 1 {
		t.Errorf("row 0 Coast = %d, want 1 (coast at sample 1 was dropped)", got)
	}
	if got := coastField(t, rows[1]); got != 0 {
		t.Errorf("row 1 Coast = %d, want 0", got)
	}
}

// At full rate the window is one sample, so aggregation must be a no-op.
func TestWriteDumpRows_FullRateUnaffectedByAggregation(t *testing.T) {
	samples := []SampleData{
		{SessionTime: 0, Throttle: 0.8},
		{SessionTime: 1.0 / 60, Throttle: 0.8, ABSActive: true},
		{SessionTime: 2.0 / 60, Throttle: 0.8},
	}
	var buf bytes.Buffer
	writeDumpRows(&buf, samples, DumpConfig{DownsampleRate: 1}, "")

	rows := strings.Split(strings.TrimSpace(buf.String()), "\n")
	want := []int{0, 1, 0}
	for i, w := range want {
		if got := absField(t, rows[i]); got != w {
			t.Errorf("row %d ABS = %d, want %d", i, got, w)
		}
	}
}

func absField(t *testing.T, row string) int   { return csvIntField(t, row, 9) }
func coastField(t *testing.T, row string) int { return csvIntField(t, row, 10) }

func csvIntField(t *testing.T, row string, idx int) int {
	t.Helper()
	fields := strings.Split(row, ",")
	if idx >= len(fields) {
		t.Fatalf("row %q has %d fields, wanted index %d", row, len(fields), idx)
	}
	n, err := strconv.Atoi(fields[idx])
	if err != nil {
		t.Fatalf("field %d of %q is not an int: %v", idx, row, err)
	}
	return n
}

func TestBuildSegmentTrace(t *testing.T) {
	laps := []Lap{dumpTestLap(3, 50), dumpTestLap(5, 55)}

	tr, err := BuildSegmentTrace(laps, dumpTestSegs(), 1, DefaultTraceConfig())
	if err != nil {
		t.Fatalf("BuildSegmentTrace error: %v", err)
	}

	if tr.Segment != "T1" || tr.Kind != string(trackmap.KindCorner) {
		t.Errorf("segment = %q (%q), want T1 (corner)", tr.Segment, tr.Kind)
	}
	if tr.SegmentIndex != 1 {
		t.Errorf("SegmentIndex = %d, want 1", tr.SegmentIndex)
	}
	if tr.RateHz != 60 {
		t.Errorf("RateHz = %d, want 60", tr.RateHz)
	}
	if tr.Columns != "Lap,"+dumpColumns {
		t.Errorf("Columns = %q, want %q", tr.Columns, "Lap,"+dumpColumns)
	}
	if len(tr.Laps) != 2 || tr.Laps[0] != 3 || tr.Laps[1] != 5 {
		t.Errorf("Laps = %v, want [3 5]", tr.Laps)
	}
	if len(tr.Rows) == 0 {
		t.Fatal("no rows")
	}

	// Every row must carry its lap number in the first column, and both laps
	// must be present — the overlay is the whole point.
	seen := map[string]int{}
	for _, row := range tr.Rows {
		fields := strings.Split(row, ",")
		if len(fields) != 12 {
			t.Fatalf("row %q: expected 12 fields, got %d", row, len(fields))
		}
		seen[fields[0]]++
	}
	if seen["3"] == 0 || seen["5"] == 0 {
		t.Errorf("expected rows from laps 3 and 5, got %v", seen)
	}
}

// The trace must match what the CSV dump would have written, byte for byte —
// that shared formatting is what stops the file and the brief disagreeing.
func TestBuildSegmentTrace_RowsMatchCSVDump(t *testing.T) {
	laps := []Lap{dumpTestLap(3, 50), dumpTestLap(5, 55)}
	cfg := DumpConfig{DownsampleRate: 3, ContextSamples: 6}

	tr, err := BuildSegmentTrace(laps, dumpTestSegs(), 1, cfg)
	if err != nil {
		t.Fatalf("BuildSegmentTrace error: %v", err)
	}

	var buf bytes.Buffer
	if err := DumpSegmentAllLapsCSV(&buf, laps, dumpTestSegs(), 1, cfg); err != nil {
		t.Fatalf("DumpSegmentAllLapsCSV error: %v", err)
	}
	var csvRows []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "Lap,") {
			continue
		}
		csvRows = append(csvRows, line)
	}

	if len(tr.Rows) != len(csvRows) {
		t.Fatalf("trace has %d rows, CSV dump has %d", len(tr.Rows), len(csvRows))
	}
	for i := range tr.Rows {
		if tr.Rows[i] != csvRows[i] {
			t.Errorf("row %d: trace %q != csv %q", i, tr.Rows[i], csvRows[i])
		}
	}
}

// A truncated lap can legitimately miss a corner; that must not cost the others.
func TestBuildSegmentTrace_SkipsLapsMissingSegment(t *testing.T) {
	good := dumpTestLap(3, 50)
	// A lap whose samples never reach the second segment (which starts at 0.5).
	short := Lap{Number: 9, Kind: KindFlying, Samples: []SampleData{{LapDistPct: 0.1}}}

	tr, err := BuildSegmentTrace([]Lap{good, short}, dumpTestSegs(), 1, DefaultTraceConfig())
	if err != nil {
		t.Fatalf("BuildSegmentTrace error: %v", err)
	}
	if len(tr.Laps) != 1 || tr.Laps[0] != 3 {
		t.Errorf("Laps = %v, want [3] — the lap missing the segment should be skipped", tr.Laps)
	}
}

func TestBuildSegmentTrace_Errors(t *testing.T) {
	segs := dumpTestSegs()
	laps := []Lap{dumpTestLap(1, 50)}

	if _, err := BuildSegmentTrace(laps, segs, 7, DefaultTraceConfig()); err == nil {
		t.Error("expected an error for an out-of-range segment index")
	}
	if _, err := BuildSegmentTrace(nil, segs, 1, DefaultTraceConfig()); err == nil {
		t.Error("expected an error when there are no laps")
	}
	// No lap reaches the segment at all: distinct from one lap missing it.
	none := []Lap{{Number: 1, Samples: []SampleData{{LapDistPct: 0.1}}}}
	if _, err := BuildSegmentTrace(none, segs, 1, DefaultTraceConfig()); err == nil {
		t.Error("expected an error when no lap has samples in the segment")
	}
}

func TestResolveSegmentList(t *testing.T) {
	segs := dumpTestSegs() // S1 (index 0), T1 (index 1)

	cases := []struct {
		spec string
		want []int
	}{
		{"T1", []int{1}},
		{"S1,T1", []int{0, 1}},
		{"2", []int{1}},            // 1-based index
		{" T1 , S1 ", []int{0, 1}}, // whitespace tolerated, sorted into track order
		{"T1,T1", []int{1}},        // duplicates collapse
		{"t1", []int{1}},           // case-insensitive, as ResolveSegmentName is
	}
	for _, c := range cases {
		got, err := ResolveSegmentList(segs, c.spec)
		if err != nil {
			t.Errorf("ResolveSegmentList(%q) error: %v", c.spec, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("ResolveSegmentList(%q) = %v, want %v", c.spec, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ResolveSegmentList(%q) = %v, want %v", c.spec, got, c.want)
				break
			}
		}
	}
}

// A typo must fail the whole list rather than quietly tracing fewer corners than
// asked for — silently dropping one reads as "that corner had no data".
func TestResolveSegmentList_UnknownFailsWholeList(t *testing.T) {
	segs := dumpTestSegs()

	if _, err := ResolveSegmentList(segs, "T1,T9"); err == nil {
		t.Error("expected an error when one entry names an unknown segment")
	}
	if _, err := ResolveSegmentList(segs, ""); err == nil {
		t.Error("expected an error for an empty spec")
	}
	if _, err := ResolveSegmentList(segs, " , "); err == nil {
		t.Error("expected an error when the spec names nothing")
	}
}
