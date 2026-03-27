package ibt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"testing"
	"time"
)

// ---- test fixture ----

// testFixture holds the constants that describe our synthetic .ibt file.
// Layout:
//
//	0x000 ( 0): rawHeader         (112 bytes)
//	0x070 (112): rawDiskHeader     ( 32 bytes)
//	0x090 (144): session info      ( 64 bytes, null-padded)
//	0x0D0 (208): var headers       (3 × 144 = 432 bytes)
//	0x280 (640): data rows         (5 × 24 = 120 bytes)
const (
	tfTickRate          = 60
	tfSessionInfoOffset = 144
	tfSessionInfoLen    = 64
	tfVarHeaderOffset   = 208
	tfDataOffset        = 640 // 208 + 3*144
	tfBufLen            = 24  // Speed(4) + Gear(4) + WheelSpeed[4](16)
	tfNumSamples        = 5
	tfLapCount          = 3
	tfStartDate         = 1_700_000_000 // arbitrary Unix timestamp
	tfStartTime         = 3600.0        // seconds since midnight
	tfEndTime           = 3700.0
)

// tfSessionYAML is the raw session info string (shorter than tfSessionInfoLen).
const tfSessionYAML = "---\nSessionNum: 0\n"

// speedForRow returns the Speed value for row i.
func speedForRow(i int) float32 { return float32(i) * 10.0 }

// gearForRow returns the Gear value for row i.
func gearForRow(i int) int32 { return int32(i) + 1 }

// wheelSpeedForRow returns WheelSpeed[j] for row i.
func wheelSpeedForRow(i, j int) float32 { return float32(i)*5.0 + float32(j) }

// buildTestFile writes a minimal valid .ibt to a temp file and returns its path.
// The caller is responsible for cleanup (handled via t.Cleanup).
func buildTestFile(t *testing.T) string {
	t.Helper()

	var buf bytes.Buffer

	// ---- rawHeader (112 bytes) ----
	hdr := rawHeader{
		Ver:               1,
		Status:            1,
		TickRate:          tfTickRate,
		SessionInfoUpdate: 42,
		SessionInfoLen:    tfSessionInfoLen,
		SessionInfoOffset: tfSessionInfoOffset,
		NumVars:           3,
		VarHeaderOffset:   tfVarHeaderOffset,
		NumBuf:            1,
		BufLen:            tfBufLen,
	}
	hdr.VarBuf[0] = rawVarBuf{TickCount: 0, BufOffset: tfDataOffset}
	mustWrite(t, &buf, binary.LittleEndian, hdr)

	// ---- rawDiskHeader (32 bytes) ----
	disk := rawDiskHeader{
		SessionStartDate:   tfStartDate,
		SessionStartTime:   tfStartTime,
		SessionEndTime:     tfEndTime,
		SessionLapCount:    tfLapCount,
		SessionRecordCount: tfNumSamples,
	}
	mustWrite(t, &buf, binary.LittleEndian, disk)

	// ---- session info (64 bytes, null-padded) ----
	si := make([]byte, tfSessionInfoLen)
	copy(si, tfSessionYAML)
	buf.Write(si)

	// ---- var headers (3 × 144 bytes) ----
	// Var 0: Speed — float32, Count=1, offset=0
	writeVarHeader(t, &buf, rawVarHeader{
		Type:   int32(VarTypeFloat),
		Offset: 0,
		Count:  1,
		Name:   padASCII32("Speed"),
		Desc:   padASCII64("Speed in m/s"),
		Unit:   padASCII32("m/s"),
	})
	// Var 1: Gear — int32, Count=1, offset=4
	writeVarHeader(t, &buf, rawVarHeader{
		Type:   int32(VarTypeInt),
		Offset: 4,
		Count:  1,
		Name:   padASCII32("Gear"),
		Desc:   padASCII64("Gear number"),
		Unit:   padASCII32(""),
	})
	// Var 2: WheelSpeed — float32, Count=4, offset=8
	writeVarHeader(t, &buf, rawVarHeader{
		Type:   int32(VarTypeFloat),
		Offset: 8,
		Count:  4,
		Name:   padASCII32("WheelSpeed"),
		Desc:   padASCII64("Wheel speeds"),
		Unit:   padASCII32("rad/s"),
	})

	// ---- data rows (5 × 24 bytes) ----
	for i := 0; i < tfNumSamples; i++ {
		row := make([]byte, tfBufLen)
		// Speed at offset 0
		binary.LittleEndian.PutUint32(row[0:], math.Float32bits(speedForRow(i)))
		// Gear at offset 4
		binary.LittleEndian.PutUint32(row[4:], uint32(gearForRow(i)))
		// WheelSpeed[0..3] at offset 8..23
		for j := 0; j < 4; j++ {
			binary.LittleEndian.PutUint32(row[8+j*4:], math.Float32bits(wheelSpeedForRow(i, j)))
		}
		buf.Write(row)
	}

	// Write to a temp file.
	tmp, err := os.CreateTemp("", "test-*.ibt")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	path := tmp.Name()
	t.Cleanup(func() { os.Remove(path) })

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		t.Fatalf("writing test ibt: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("closing temp file: %v", err)
	}
	return path
}

// ---- builder helpers ----

func mustWrite(t *testing.T, w *bytes.Buffer, order binary.ByteOrder, v any) {
	t.Helper()
	if err := binary.Write(w, order, v); err != nil {
		t.Fatalf("binary.Write: %v", err)
	}
}

func writeVarHeader(t *testing.T, w *bytes.Buffer, rvh rawVarHeader) {
	t.Helper()
	mustWrite(t, w, binary.LittleEndian, rvh)
}

func padASCII32(s string) [32]byte {
	var b [32]byte
	copy(b[:], s)
	return b
}

func padASCII64(s string) [64]byte {
	var b [64]byte
	copy(b[:], s)
	return b
}

// ---- tests ----

func TestOpen_HeaderFields(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	h := f.Header()
	if h.Ver != 1 {
		t.Errorf("Ver = %d, want 1", h.Ver)
	}
	if h.TickRate != tfTickRate {
		t.Errorf("TickRate = %d, want %d", h.TickRate, tfTickRate)
	}
	if h.NumVars != 3 {
		t.Errorf("NumVars = %d, want 3", h.NumVars)
	}
	if h.BufLen != tfBufLen {
		t.Errorf("BufLen = %d, want %d", h.BufLen, tfBufLen)
	}
	if h.DataOffset != tfDataOffset {
		t.Errorf("DataOffset = %d, want %d", h.DataOffset, tfDataOffset)
	}
}

func TestOpen_DiskHeaderFields(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	dh := f.DiskHeader()
	wantDate := time.Unix(tfStartDate, 0).UTC()
	if !dh.SessionStartDate.Equal(wantDate) {
		t.Errorf("SessionStartDate = %v, want %v", dh.SessionStartDate, wantDate)
	}
	if dh.SessionStartTime != tfStartTime {
		t.Errorf("SessionStartTime = %v, want %v", dh.SessionStartTime, tfStartTime)
	}
	if dh.SessionEndTime != tfEndTime {
		t.Errorf("SessionEndTime = %v, want %v", dh.SessionEndTime, tfEndTime)
	}
	if dh.SessionLapCount != tfLapCount {
		t.Errorf("SessionLapCount = %d, want %d", dh.SessionLapCount, tfLapCount)
	}
	if dh.SessionRecordCount != tfNumSamples {
		t.Errorf("SessionRecordCount = %d, want %d", dh.SessionRecordCount, tfNumSamples)
	}
}

func TestOpen_SessionInfo(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	if got := f.SessionInfo(); got != tfSessionYAML {
		t.Errorf("SessionInfo = %q, want %q", got, tfSessionYAML)
	}
}

func TestOpen_VarDefs(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	vars := f.Vars()
	if len(vars) != 3 {
		t.Fatalf("len(Vars) = %d, want 3", len(vars))
	}

	tests := []struct {
		name    string
		varType VarType
		count   int
		unit    string
		offset  int
	}{
		{"Speed", VarTypeFloat, 1, "m/s", 0},
		{"Gear", VarTypeInt, 1, "", 4},
		{"WheelSpeed", VarTypeFloat, 4, "rad/s", 8},
	}
	for i, tc := range tests {
		vd := vars[i]
		if vd.Name != tc.name {
			t.Errorf("vars[%d].Name = %q, want %q", i, vd.Name, tc.name)
		}
		if vd.Type != tc.varType {
			t.Errorf("vars[%d].Type = %v, want %v", i, vd.Type, tc.varType)
		}
		if vd.Count != tc.count {
			t.Errorf("vars[%d].Count = %d, want %d", i, vd.Count, tc.count)
		}
		if vd.Unit != tc.unit {
			t.Errorf("vars[%d].Unit = %q, want %q", i, vd.Unit, tc.unit)
		}
		if vd.Offset != tc.offset {
			t.Errorf("vars[%d].Offset = %d, want %d", i, vd.Offset, tc.offset)
		}
	}
}

func TestOpen_VarDefLookup(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	if _, ok := f.VarDef("Speed"); !ok {
		t.Error("VarDef(\"Speed\"): expected ok=true")
	}
	if _, ok := f.VarDef("NoSuchVar"); ok {
		t.Error("VarDef(\"NoSuchVar\"): expected ok=false")
	}
}

func TestNumSamples(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	if n := f.NumSamples(); n != tfNumSamples {
		t.Errorf("NumSamples = %d, want %d", n, tfNumSamples)
	}
}

func TestSample_Float32(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	for i := 0; i < tfNumSamples; i++ {
		s, err := f.Sample(i)
		if err != nil {
			t.Fatalf("Sample(%d): %v", i, err)
		}
		got, ok := s.Float32("Speed")
		if !ok {
			t.Errorf("row %d: Float32(\"Speed\") ok=false", i)
			continue
		}
		want := speedForRow(i)
		if got != want {
			t.Errorf("row %d: Speed = %v, want %v", i, got, want)
		}
	}
}

func TestSample_Int(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	for i := 0; i < tfNumSamples; i++ {
		s, err := f.Sample(i)
		if err != nil {
			t.Fatalf("Sample(%d): %v", i, err)
		}
		got, ok := s.Int("Gear")
		if !ok {
			t.Errorf("row %d: Int(\"Gear\") ok=false", i)
			continue
		}
		want := gearForRow(i)
		if got != want {
			t.Errorf("row %d: Gear = %v, want %v", i, got, want)
		}
	}
}

func TestSample_Float32s(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	for i := 0; i < tfNumSamples; i++ {
		s, err := f.Sample(i)
		if err != nil {
			t.Fatalf("Sample(%d): %v", i, err)
		}
		got, ok := s.Float32s("WheelSpeed")
		if !ok {
			t.Errorf("row %d: Float32s(\"WheelSpeed\") ok=false", i)
			continue
		}
		if len(got) != 4 {
			t.Errorf("row %d: len(WheelSpeed) = %d, want 4", i, len(got))
			continue
		}
		for j := 0; j < 4; j++ {
			want := wheelSpeedForRow(i, j)
			if got[j] != want {
				t.Errorf("row %d: WheelSpeed[%d] = %v, want %v", i, j, got[j], want)
			}
		}
	}
}

func TestSample_WrongType(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	s, err := f.Sample(0)
	if err != nil {
		t.Fatalf("Sample(0): %v", err)
	}

	// Gear is int32; asking for Float32 should return (0, false).
	if v, ok := s.Float32("Gear"); ok || v != 0 {
		t.Errorf("Float32(\"Gear\") = (%v, %v), want (0, false)", v, ok)
	}
	// Speed is float32; asking for Int should return (0, false).
	if v, ok := s.Int("Speed"); ok || v != 0 {
		t.Errorf("Int(\"Speed\") = (%v, %v), want (0, false)", v, ok)
	}
}

func TestSample_UnknownName(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	s, err := f.Sample(0)
	if err != nil {
		t.Fatalf("Sample(0): %v", err)
	}

	if v, ok := s.Float32("NoSuchVar"); ok || v != 0 {
		t.Errorf("Float32(\"NoSuchVar\") = (%v, %v), want (0, false)", v, ok)
	}
	if v, ok := s.Int("NoSuchVar"); ok || v != 0 {
		t.Errorf("Int(\"NoSuchVar\") = (%v, %v), want (0, false)", v, ok)
	}
	if v, ok := s.Bool("NoSuchVar"); ok || v {
		t.Errorf("Bool(\"NoSuchVar\") = (%v, %v), want (false, false)", v, ok)
	}
	if v, ok := s.Float64("NoSuchVar"); ok || v != 0 {
		t.Errorf("Float64(\"NoSuchVar\") = (%v, %v), want (0, false)", v, ok)
	}
	if v, ok := s.BitField("NoSuchVar"); ok || v != 0 {
		t.Errorf("BitField(\"NoSuchVar\") = (%v, %v), want (0, false)", v, ok)
	}
}

func TestSample_OutOfRange(t *testing.T) {
	path := buildTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	if _, err := f.Sample(-1); err == nil {
		t.Error("Sample(-1): expected error, got nil")
	}
	if _, err := f.Sample(tfNumSamples); err == nil {
		t.Errorf("Sample(%d): expected error, got nil", tfNumSamples)
	}
}

func TestOpen_InvalidVersion(t *testing.T) {
	path := buildTestFile(t)

	// Overwrite the Ver field (first 4 bytes) with 99.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading test file: %v", err)
	}
	binary.LittleEndian.PutUint32(data[0:], 99)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing modified test file: %v", err)
	}

	_, err = Open(path)
	if err == nil {
		t.Fatal("Open: expected error for version 99, got nil")
	}
	if !isInvalidFormat(err) {
		t.Errorf("Open: expected ErrInvalidFormat in error chain, got %v", err)
	}
}

func TestOpen_FileNotFound(t *testing.T) {
	_, err := Open("nonexistent_file_that_does_not_exist.ibt")
	if err == nil {
		t.Fatal("Open: expected error for missing file, got nil")
	}
}

func TestOpen_SessionInfoTooLarge(t *testing.T) {
	path := buildTestFile(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading test file: %v", err)
	}
	// SessionInfoLen is the 5th int32 in rawHeader — byte offset 16.
	binary.LittleEndian.PutUint32(data[16:], 20*1024*1024) // 20 MB > 10 MB max
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing modified test file: %v", err)
	}

	_, err = Open(path)
	if err == nil {
		t.Fatal("Open: expected error for oversized SessionInfoLen, got nil")
	}
	if !isInvalidFormat(err) {
		t.Errorf("Open: expected ErrInvalidFormat in error chain, got %v", err)
	}
}

func TestOpen_NumVarsTooLarge(t *testing.T) {
	path := buildTestFile(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading test file: %v", err)
	}
	// NumVars is the 7th int32 in rawHeader — byte offset 24.
	binary.LittleEndian.PutUint32(data[24:], 5000) // 5000 > 4096 max
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing modified test file: %v", err)
	}

	_, err = Open(path)
	if err == nil {
		t.Fatal("Open: expected error for oversized NumVars, got nil")
	}
	if !isInvalidFormat(err) {
		t.Errorf("Open: expected ErrInvalidFormat in error chain, got %v", err)
	}
}

// isInvalidFormat reports whether err wraps ErrInvalidFormat.
func isInvalidFormat(err error) bool {
	return errors.Is(err, ErrInvalidFormat)
}
