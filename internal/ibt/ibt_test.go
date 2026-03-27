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

// ---- trimASCII ----

func TestTrimASCII(t *testing.T) {
	cases := []struct {
		input []byte
		want  string
	}{
		{[]byte{'h', 'e', 'l', 'l', 'o', 0, 0, 0}, "hello"}, // null-terminated
		{[]byte{'a', 'b', 'c'}, "abc"},                        // no null byte — else branch
		{[]byte{0, 'x'}, ""},                                  // null at index 0
		{[]byte{}, ""},                                        // empty slice
	}
	for _, tc := range cases {
		if got := trimASCII(tc.input); got != tc.want {
			t.Errorf("trimASCII(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---- VarType ----

func TestVarType_String(t *testing.T) {
	cases := []struct {
		vt   VarType
		want string
	}{
		{VarTypeChar, "char"},
		{VarTypeBool, "bool"},
		{VarTypeInt, "int32"},
		{VarTypeBitField, "bitField"},
		{VarTypeFloat, "float32"},
		{VarTypeDouble, "float64"},
		{VarType(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.vt.String(); got != tc.want {
			t.Errorf("VarType(%d).String() = %q, want %q", tc.vt, got, tc.want)
		}
	}
}

func TestVarType_ElemSize(t *testing.T) {
	cases := []struct {
		vt   VarType
		want int
	}{
		{VarTypeChar, 1},
		{VarTypeBool, 1},
		{VarTypeInt, 4},
		{VarTypeBitField, 4},
		{VarTypeFloat, 4},
		{VarTypeDouble, 8},
		{VarType(99), 0},
	}
	for _, tc := range cases {
		if got := tc.vt.elemSize(); got != tc.want {
			t.Errorf("VarType(%d).elemSize() = %d, want %d", tc.vt, got, tc.want)
		}
	}
}

// ---- parse error paths ----

func TestOpen_NumBufNotOne(t *testing.T) {
	path := buildTestFile(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading test file: %v", err)
	}
	// NumBuf is the 9th int32 in rawHeader — byte offset 32.
	binary.LittleEndian.PutUint32(data[32:], 2)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing modified test file: %v", err)
	}
	_, err = Open(path)
	if err == nil {
		t.Fatal("Open: expected error for NumBuf=2, got nil")
	}
	if !isInvalidFormat(err) {
		t.Errorf("Open: expected ErrInvalidFormat, got %v", err)
	}
}

func TestOpen_UnknownVarType(t *testing.T) {
	path := buildTestFile(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading test file: %v", err)
	}
	// First var header's Type field sits at tfVarHeaderOffset (208).
	binary.LittleEndian.PutUint32(data[tfVarHeaderOffset:], 99)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing modified test file: %v", err)
	}
	_, err = Open(path)
	if err == nil {
		t.Fatal("Open: expected error for unknown var type, got nil")
	}
	if !isInvalidFormat(err) {
		t.Errorf("Open: expected ErrInvalidFormat, got %v", err)
	}
}

func TestOpen_VarExceedsRow(t *testing.T) {
	path := buildTestFile(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading test file: %v", err)
	}
	// First var header: Type=float32(4), Offset=0, Count at tfVarHeaderOffset+8.
	// Set Count=100 so 100×4=400 >> BufLen(24).
	binary.LittleEndian.PutUint32(data[tfVarHeaderOffset+8:], 100)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing modified test file: %v", err)
	}
	_, err = Open(path)
	if err == nil {
		t.Fatal("Open: expected error for var exceeding row size, got nil")
	}
	if !isInvalidFormat(err) {
		t.Errorf("Open: expected ErrInvalidFormat, got %v", err)
	}
}

// ---- extended test file (float64, bool, bitfield, int array, float64 array) ----

const (
	extNumVars    = 5
	extBufLen     = 44 // Timing(8)+Active(1)+pad(3)+Flags(4)+Tyres3(12)+Heights2(16)
	extDataOffset = tfVarHeaderOffset + extNumVars*144 // 208 + 720 = 928
	extNumSamples = 3

	extOffsetTiming  = 0  // float64 (8 bytes)
	extOffsetActive  = 8  // bool    (1 byte)
	// 3 bytes implicit padding to align Flags to 4
	extOffsetFlags   = 12 // bitfield (4 bytes)
	extOffsetTyres   = 16 // int32[3] (12 bytes)
	extOffsetHeights = 28 // float64[2] (16 bytes)
)

func timingForRow(i int) float64    { return float64(i) * 2.5 }
func activeForRow(i int) bool       { return i%2 == 0 }
func flagsForRow(i int) uint32      { return 1 << uint(i) }
func tyresForRow(i, j int) int32    { return int32(i*10 + j) }
func heightsForRow(i, j int) float64 { return float64(i)*2.0 + float64(j)*0.5 }

func buildExtendedTestFile(t *testing.T) string {
	t.Helper()

	var buf bytes.Buffer

	hdr := rawHeader{
		Ver: 1, Status: 1, TickRate: tfTickRate,
		SessionInfoLen: tfSessionInfoLen, SessionInfoOffset: tfSessionInfoOffset,
		NumVars: extNumVars, VarHeaderOffset: tfVarHeaderOffset,
		NumBuf: 1, BufLen: extBufLen,
	}
	hdr.VarBuf[0] = rawVarBuf{TickCount: 0, BufOffset: extDataOffset}
	mustWrite(t, &buf, binary.LittleEndian, hdr)

	disk := rawDiskHeader{
		SessionStartDate: tfStartDate, SessionStartTime: tfStartTime,
		SessionEndTime: tfEndTime, SessionLapCount: 1, SessionRecordCount: extNumSamples,
	}
	mustWrite(t, &buf, binary.LittleEndian, disk)

	si := make([]byte, tfSessionInfoLen)
	copy(si, tfSessionYAML)
	buf.Write(si)

	writeVarHeader(t, &buf, rawVarHeader{
		Type: int32(VarTypeDouble), Offset: extOffsetTiming, Count: 1,
		Name: padASCII32("Timing"), Desc: padASCII64("Session time"), Unit: padASCII32("s"),
	})
	writeVarHeader(t, &buf, rawVarHeader{
		Type: int32(VarTypeBool), Offset: extOffsetActive, Count: 1,
		Name: padASCII32("Active"), Desc: padASCII64("ABS active"), Unit: padASCII32(""),
	})
	writeVarHeader(t, &buf, rawVarHeader{
		Type: int32(VarTypeBitField), Offset: extOffsetFlags, Count: 1,
		Name: padASCII32("Flags"), Desc: padASCII64("Status flags"), Unit: padASCII32(""),
	})
	writeVarHeader(t, &buf, rawVarHeader{
		Type: int32(VarTypeInt), Offset: extOffsetTyres, Count: 3,
		Name: padASCII32("Tyres"), Desc: padASCII64("Tyre integers"), Unit: padASCII32(""),
	})
	writeVarHeader(t, &buf, rawVarHeader{
		Type: int32(VarTypeDouble), Offset: extOffsetHeights, Count: 2,
		Name: padASCII32("Heights"), Desc: padASCII64("Ride heights"), Unit: padASCII32("m"),
	})

	for i := 0; i < extNumSamples; i++ {
		row := make([]byte, extBufLen)
		binary.LittleEndian.PutUint64(row[extOffsetTiming:], math.Float64bits(timingForRow(i)))
		if activeForRow(i) {
			row[extOffsetActive] = 1
		}
		binary.LittleEndian.PutUint32(row[extOffsetFlags:], flagsForRow(i))
		for j := 0; j < 3; j++ {
			binary.LittleEndian.PutUint32(row[extOffsetTyres+j*4:], uint32(tyresForRow(i, j)))
		}
		for j := 0; j < 2; j++ {
			binary.LittleEndian.PutUint64(row[extOffsetHeights+j*8:], math.Float64bits(heightsForRow(i, j)))
		}
		buf.Write(row)
	}

	tmp, err := os.CreateTemp("", "test-ext-*.ibt")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	path := tmp.Name()
	t.Cleanup(func() { os.Remove(path) })
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		t.Fatalf("writing extended ibt: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("closing temp file: %v", err)
	}
	return path
}

// ---- Float64 (scalar) ----

func TestSample_Float64(t *testing.T) {
	path := buildExtendedTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	for i := 0; i < extNumSamples; i++ {
		s, err := f.Sample(i)
		if err != nil {
			t.Fatalf("Sample(%d): %v", i, err)
		}
		got, ok := s.Float64("Timing")
		if !ok {
			t.Errorf("row %d: Float64(\"Timing\") ok=false", i)
			continue
		}
		if got != timingForRow(i) {
			t.Errorf("row %d: Timing = %v, want %v", i, got, timingForRow(i))
		}
		// Wrong type: asking float64 var as Float32 should fail.
		if v, ok2 := s.Float32("Timing"); ok2 || v != 0 {
			t.Errorf("row %d: Float32(\"Timing\") = (%v, %v), want (0, false)", i, v, ok2)
		}
	}
}

// ---- Bool ----

func TestSample_Bool(t *testing.T) {
	path := buildExtendedTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	for i := 0; i < extNumSamples; i++ {
		s, err := f.Sample(i)
		if err != nil {
			t.Fatalf("Sample(%d): %v", i, err)
		}
		got, ok := s.Bool("Active")
		if !ok {
			t.Errorf("row %d: Bool(\"Active\") ok=false", i)
			continue
		}
		if got != activeForRow(i) {
			t.Errorf("row %d: Active = %v, want %v", i, got, activeForRow(i))
		}
		// Wrong type: asking bool var as Float32 should fail.
		if v, ok2 := s.Float32("Active"); ok2 || v != 0 {
			t.Errorf("row %d: Float32(\"Active\") = (%v, %v), want (0, false)", i, v, ok2)
		}
	}
}

// ---- BitField ----

func TestSample_BitField(t *testing.T) {
	path := buildExtendedTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	for i := 0; i < extNumSamples; i++ {
		s, err := f.Sample(i)
		if err != nil {
			t.Fatalf("Sample(%d): %v", i, err)
		}
		got, ok := s.BitField("Flags")
		if !ok {
			t.Errorf("row %d: BitField(\"Flags\") ok=false", i)
			continue
		}
		if got != flagsForRow(i) {
			t.Errorf("row %d: Flags = %v, want %v", i, got, flagsForRow(i))
		}
		// Wrong type: asking bitfield var as Int should fail.
		if v, ok2 := s.Int("Flags"); ok2 || v != 0 {
			t.Errorf("row %d: Int(\"Flags\") = (%v, %v), want (0, false)", i, v, ok2)
		}
	}
}

// ---- Ints (array) ----

func TestSample_Ints(t *testing.T) {
	path := buildExtendedTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	for i := 0; i < extNumSamples; i++ {
		s, err := f.Sample(i)
		if err != nil {
			t.Fatalf("Sample(%d): %v", i, err)
		}
		got, ok := s.Ints("Tyres")
		if !ok {
			t.Errorf("row %d: Ints(\"Tyres\") ok=false", i)
			continue
		}
		if len(got) != 3 {
			t.Errorf("row %d: len(Tyres) = %d, want 3", i, len(got))
			continue
		}
		for j := 0; j < 3; j++ {
			if got[j] != tyresForRow(i, j) {
				t.Errorf("row %d: Tyres[%d] = %v, want %v", i, j, got[j], tyresForRow(i, j))
			}
		}
		// Wrong type: Ints on a float64 var should fail.
		if v, ok2 := s.Ints("Timing"); ok2 || v != nil {
			t.Errorf("row %d: Ints(\"Timing\") = (%v, %v), want (nil, false)", i, v, ok2)
		}
	}
}

// ---- Float64s (array) ----

func TestSample_Float64s(t *testing.T) {
	path := buildExtendedTestFile(t)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	for i := 0; i < extNumSamples; i++ {
		s, err := f.Sample(i)
		if err != nil {
			t.Fatalf("Sample(%d): %v", i, err)
		}
		got, ok := s.Float64s("Heights")
		if !ok {
			t.Errorf("row %d: Float64s(\"Heights\") ok=false", i)
			continue
		}
		if len(got) != 2 {
			t.Errorf("row %d: len(Heights) = %d, want 2", i, len(got))
			continue
		}
		for j := 0; j < 2; j++ {
			if got[j] != heightsForRow(i, j) {
				t.Errorf("row %d: Heights[%d] = %v, want %v", i, j, got[j], heightsForRow(i, j))
			}
		}
		// Wrong type: Float64s on an int32 var should fail.
		if v, ok2 := s.Float64s("Tyres"); ok2 || v != nil {
			t.Errorf("row %d: Float64s(\"Tyres\") = (%v, %v), want (nil, false)", i, v, ok2)
		}
	}
}
