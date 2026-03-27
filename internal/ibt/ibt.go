// Package ibt provides a parser for iRacing binary telemetry (.ibt) files.
//
// Basic usage:
//
//	f, err := ibt.Open("session.ibt")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer f.Close()
//
//	for i := 0; i < f.NumSamples(); i++ {
//	    s, err := f.Sample(i)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    speed, _ := s.Float32("Speed")
//	    fmt.Println(speed)
//	}
package ibt

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrInvalidFormat is returned when the file fails structural validation.
var ErrInvalidFormat = errors.New("ibt: invalid format")

// ---- internal binary structs (must match wire layout exactly) ----

// rawVarBuf is the 16-byte buffer descriptor embedded in rawHeader.
type rawVarBuf struct {
	TickCount int32
	BufOffset int32
	Pad       [2]int32
}

// rawHeader maps to irsdk_header (112 bytes at file offset 0).
type rawHeader struct {
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
	VarBuf            [4]rawVarBuf
}

// rawDiskHeader maps to irsdk_diskSubHeader (32 bytes at file offset 112).
type rawDiskHeader struct {
	SessionStartDate   int64
	SessionStartTime   float64
	SessionEndTime     float64
	SessionLapCount    int32
	SessionRecordCount int32
}

// rawVarHeader maps to irsdk_varHeader (144 bytes each).
type rawVarHeader struct {
	Type        int32
	Offset      int32
	Count       int32
	CountAsTime bool
	Pad         [3]byte
	Name        [32]byte
	Desc        [64]byte
	Unit        [32]byte
}

// ---- public types ----

// Header contains the parsed irsdk_header metadata.
type Header struct {
	Ver               int
	TickRate          int
	SessionInfoUpdate int
	NumVars           int
	BufLen            int
	// DataOffset is the file offset of the first data row.
	DataOffset int
}

// DiskHeader contains the parsed irsdk_diskSubHeader metadata.
type DiskHeader struct {
	SessionStartDate   time.Time
	SessionStartTime   float64 // seconds since midnight
	SessionEndTime     float64 // seconds since midnight
	SessionLapCount    int
	SessionRecordCount int
}

// VarDef describes one telemetry channel (variable).
type VarDef struct {
	Type        VarType
	Offset      int // byte offset within a data row
	Count       int // array length; 1 for scalar variables
	CountAsTime bool
	Name        string
	Desc        string
	Unit        string
}

// ---- File ----

// File holds a parsed .ibt file open for reading.
// Call Close when done to release the underlying OS file handle.
type File struct {
	f           *os.File
	hdr         Header
	diskHdr     DiskHeader
	sessionInfo string
	vars        []VarDef
	varIndex    map[string]*VarDef
}

// Open parses the .ibt file at path, reading all header and variable
// definition data. The file is kept open; call Close when done.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("ibt: opening file: %w", err)
	}

	ibtf, err := parse(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return ibtf, nil
}

// parse reads and validates the binary structure from an open file.
func parse(f *os.File) (*File, error) {
	// 1. Read irsdk_header (112 bytes at offset 0).
	var rawHdr rawHeader
	if err := binary.Read(f, binary.LittleEndian, &rawHdr); err != nil {
		return nil, fmt.Errorf("ibt: reading header: %w", err)
	}
	if rawHdr.Ver != 1 && rawHdr.Ver != 2 {
		return nil, fmt.Errorf("ibt: version %d not supported: %w", rawHdr.Ver, ErrInvalidFormat)
	}
	if rawHdr.NumBuf != 1 {
		return nil, fmt.Errorf("ibt: NumBuf %d not supported (expected 1): %w", rawHdr.NumBuf, ErrInvalidFormat)
	}

	// 2. Read irsdk_diskSubHeader (32 bytes at offset 112, file is already there).
	var rawDisk rawDiskHeader
	if err := binary.Read(f, binary.LittleEndian, &rawDisk); err != nil {
		return nil, fmt.Errorf("ibt: reading disk header: %w", err)
	}

	// 3. Read session info YAML string.
	var sessionInfo string
	if rawHdr.SessionInfoLen > 0 {
		if _, err := f.Seek(int64(rawHdr.SessionInfoOffset), 0); err != nil {
			return nil, fmt.Errorf("ibt: seeking to session info: %w", err)
		}
		buf := make([]byte, rawHdr.SessionInfoLen)
		if _, err := f.Read(buf); err != nil {
			return nil, fmt.Errorf("ibt: reading session info: %w", err)
		}
		sessionInfo = string(bytes.TrimRight(buf, "\x00"))
	}

	// 4. Read variable headers.
	if _, err := f.Seek(int64(rawHdr.VarHeaderOffset), 0); err != nil {
		return nil, fmt.Errorf("ibt: seeking to var headers: %w", err)
	}
	vars := make([]VarDef, rawHdr.NumVars)
	for i := range vars {
		var rvh rawVarHeader
		if err := binary.Read(f, binary.LittleEndian, &rvh); err != nil {
			return nil, fmt.Errorf("ibt: reading var header %d: %w", i, err)
		}
		vd := VarDef{
			Type:        VarType(rvh.Type),
			Offset:      int(rvh.Offset),
			Count:       int(rvh.Count),
			CountAsTime: rvh.CountAsTime,
			Name:        trimASCII(rvh.Name[:]),
			Desc:        trimASCII(rvh.Desc[:]),
			Unit:        trimASCII(rvh.Unit[:]),
		}
		// 5. Bounds check: ensure the var's data fits within one row.
		elemSz := vd.Type.elemSize()
		if elemSz == 0 {
			return nil, fmt.Errorf("ibt: var %q has unknown type %d: %w", vd.Name, vd.Type, ErrInvalidFormat)
		}
		end := vd.Offset + vd.Count*elemSz
		if end > int(rawHdr.BufLen) {
			return nil, fmt.Errorf("ibt: var %q exceeds row size (%d > %d): %w", vd.Name, end, rawHdr.BufLen, ErrInvalidFormat)
		}
		vars[i] = vd
	}

	// 6. Build name → *VarDef index.
	varIndex := make(map[string]*VarDef, len(vars))
	for i := range vars {
		varIndex[vars[i].Name] = &vars[i]
	}

	return &File{
		f: f,
		hdr: Header{
			Ver:               int(rawHdr.Ver),
			TickRate:          int(rawHdr.TickRate),
			SessionInfoUpdate: int(rawHdr.SessionInfoUpdate),
			NumVars:           int(rawHdr.NumVars),
			BufLen:            int(rawHdr.BufLen),
			DataOffset:        int(rawHdr.VarBuf[0].BufOffset),
		},
		diskHdr: DiskHeader{
			SessionStartDate:   time.Unix(rawDisk.SessionStartDate, 0).UTC(),
			SessionStartTime:   rawDisk.SessionStartTime,
			SessionEndTime:     rawDisk.SessionEndTime,
			SessionLapCount:    int(rawDisk.SessionLapCount),
			SessionRecordCount: int(rawDisk.SessionRecordCount),
		},
		sessionInfo: sessionInfo,
		vars:        vars,
		varIndex:    varIndex,
	}, nil
}

// Close releases the underlying OS file handle.
func (f *File) Close() error {
	return f.f.Close()
}

// Header returns a copy of the parsed irsdk_header metadata.
func (f *File) Header() Header {
	return f.hdr
}

// DiskHeader returns a copy of the parsed irsdk_diskSubHeader metadata.
func (f *File) DiskHeader() DiskHeader {
	return f.diskHdr
}

// SessionInfo returns the raw YAML session info string (null bytes trimmed).
func (f *File) SessionInfo() string {
	return f.sessionInfo
}

// Vars returns a copy of the variable definition slice.
func (f *File) Vars() []VarDef {
	out := make([]VarDef, len(f.vars))
	copy(out, f.vars)
	return out
}

// VarDef returns the definition for the named variable, or (VarDef{}, false)
// if no variable with that name exists.
func (f *File) VarDef(name string) (VarDef, bool) {
	vd, ok := f.varIndex[name]
	if !ok {
		return VarDef{}, false
	}
	return *vd, true
}

// NumSamples returns the total number of data rows in the file.
func (f *File) NumSamples() int {
	return f.diskHdr.SessionRecordCount
}

// Sample reads and returns the nth data row (0-based).
// It is safe to call Sample concurrently from multiple goroutines.
func (f *File) Sample(n int) (Sample, error) {
	if n < 0 || n >= f.diskHdr.SessionRecordCount {
		return Sample{}, fmt.Errorf("ibt: sample index %d out of range [0, %d)", n, f.diskHdr.SessionRecordCount)
	}
	offset := int64(f.hdr.DataOffset) + int64(n)*int64(f.hdr.BufLen)
	raw := make([]byte, f.hdr.BufLen)
	if _, err := f.f.ReadAt(raw, offset); err != nil {
		return Sample{}, fmt.Errorf("ibt: reading sample %d: %w", n, err)
	}
	return Sample{raw: raw, vars: f.varIndex}, nil
}

// ---- helpers ----

// trimASCII converts a null-padded byte slice to a trimmed string.
func trimASCII(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
