//go:build windows

// Package iracing reads live telemetry from iRacing's shared memory interface.
package iracing

import (
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// LiveData holds a snapshot of iRacing live telemetry.
// All fields are zero/false when iRacing is not connected.
// ErrMsg is non-empty when ReadLiveData failed for a diagnosable reason.
//
// CarIdx* slices are indexed by CarIdx (0..N-1 where N is iRacing's published
// car-array length, typically 64). Entries whose LapDistPct is negative are
// invalid — iRacing uses that to mean "car not on track / pitted / disconnected".
type LiveData struct {
	Connected   bool
	SessionTime float64
	LapDistPct  float32 // 0.0–1.0 fraction of lap distance from S/F line (player's car)
	Track       string
	Car         string
	ErrMsg      string // diagnostic; empty on success

	MyCarIdx            int32 // player's CarIdx from session YAML (−1 if unresolved)
	CarIdxLapDistPct    []float32
	CarIdxLapCompleted  []int32
	CarIdxEstTime       []float32
	CarIdxPosition      []int32
	CarIdxClassPosition []int32

	// Drivers is keyed by CarIdx. Only populated for slots that had a driver
	// block in the session YAML.
	Drivers map[int32]DriverInfo
}

// iRacing shared memory layout constants.
const (
	// Exactly matches IRSDK_MEMMAPFILENAME in iRacing's public SDK header
	// (irsdk_defines.h). Earlier versions of this file used the shorter
	// "Local\\IRSDKMemMap" — wrong — which caused OpenFileMappingW to return
	// ERROR_FILE_NOT_FOUND even while iRacing was running on-track.
	memMapName = "Local\\IRSDKMemMapFileName"

	// irsdk_header field offsets (bytes from base of mapped memory)
	hdrOffStatus         = 4
	hdrOffSessionInfoLen = 16
	hdrOffSessionInfoOff = 20
	hdrOffNumVars        = 24
	hdrOffVarHeaderOff   = 28
	hdrOffBufCount       = 32
	hdrOffVarBufsStart   = 48 // start of varBuf[4] array

	// Each varBuf entry: tickCount(4) + bufOffset(4) + pad[2](8) = 16 bytes
	varBufSize      = 16
	varBufOffTick   = 0
	varBufOffBufOff = 4

	// Variable header size and field offsets within each header
	varHeaderSize   = 144
	vhOffType       = 0
	vhOffDataOffset = 4
	vhOffCount      = 8  // number of array entries (1 for scalars, >1 for CarIdx* arrays)
	vhOffName       = 16 // 32 bytes, null-terminated

	// iRacing variable type codes
	varTypeInt    = 2
	varTypeFloat  = 4
	varTypeDouble = 5

	// irsdk_StatusField
	iRSDKConnected = 1

	// Windows memory mapping access
	fileMapRead = 0x0004

	// maxSessionInfoBytes caps the session YAML read. Must match the array size in
	// the unsafe slice expression below — if you change one, change the other.
	maxSessionInfoBytes = 1 << 20 // 1 MB; actual iRacing YAML is ~50–200 KB
)

var (
	modKernel32         = syscall.NewLazyDLL("kernel32.dll")
	procOpenFileMapping = modKernel32.NewProc("OpenFileMappingW")
	procMapViewOfFile   = modKernel32.NewProc("MapViewOfFile")
	procUnmapViewOfFile = modKernel32.NewProc("UnmapViewOfFile")
	procCloseHandle     = modKernel32.NewProc("CloseHandle")
)

type varInfo struct {
	varType    int32
	dataOffset int32
	count      int32
}

// ReadLiveData reads a snapshot from iRacing shared memory.
// Returns a zero-value LiveData (Connected=false) if iRacing is not running or not on track.
// Returns a LiveData with a non-empty ErrMsg if it failed for a diagnosable reason.
func ReadLiveData() LiveData {
	namePtr, _ := syscall.UTF16PtrFromString(memMapName)
	handle, _, lastErr := procOpenFileMapping.Call(fileMapRead, 0, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return LiveData{ErrMsg: "OpenFileMappingW: " + lastErr.Error()}
	}
	defer procCloseHandle.Call(handle)

	baseAddr, _, lastErr := procMapViewOfFile.Call(handle, fileMapRead, 0, 0, 0)
	if baseAddr == 0 {
		return LiveData{ErrMsg: "MapViewOfFile: " + lastErr.Error()}
	}
	defer procUnmapViewOfFile.Call(baseAddr)

	// Convert to unsafe.Pointer immediately — all subsequent reads use unsafe.Add
	// which satisfies Go's unsafe.Pointer rules (no uintptr arithmetic).
	// The uintptr→Pointer conversion is safe per unsafe.Pointer Rule 4 (syscall result).
	base := unsafe.Pointer(baseAddr) //nolint:govet

	// Check connection status — iRacing sets this to iRSDKConnected(1) when a
	// session is live. It is 0 in menus, replays, or between sessions.
	status := readInt32(base, hdrOffStatus)
	if status&iRSDKConnected == 0 {
		return LiveData{ErrMsg: "iRacing status not connected (status=" + itoa(status) + ")"}
	}

	numVars := int(readInt32(base, hdrOffNumVars))
	varHeaderOff := int(readInt32(base, hdrOffVarHeaderOff))

	// Build variable lookup map
	vars := make(map[string]varInfo, numVars)
	for i := 0; i < numVars; i++ {
		vhBase := varHeaderOff + i*varHeaderSize
		vType := readInt32(base, vhBase+vhOffType)
		dataOff := readInt32(base, vhBase+vhOffDataOffset)
		count := readInt32(base, vhBase+vhOffCount)
		nameBytes := (*[32]byte)(unsafe.Add(base, vhBase+vhOffName))[:]
		name := nullTermString(nameBytes)
		if name != "" {
			vars[name] = varInfo{varType: vType, dataOffset: dataOff, count: count}
		}
	}

	// Find most-recent data buffer (highest tickCount)
	bufCount := int(readInt32(base, hdrOffBufCount))
	if bufCount <= 0 || bufCount > 4 {
		bufCount = 4
	}
	bestTick := int32(-1)
	bestBufOff := int32(0)
	for i := 0; i < bufCount; i++ {
		entryBase := hdrOffVarBufsStart + i*varBufSize
		tick := readInt32(base, entryBase+varBufOffTick)
		if tick > bestTick {
			bestTick = tick
			bestBufOff = readInt32(base, entryBase+varBufOffBufOff)
		}
	}

	dataBase := int(bestBufOff)

	ld := LiveData{Connected: true}

	if v, ok := vars["SessionTime"]; ok && v.varType == varTypeDouble {
		ld.SessionTime = readFloat64(base, dataBase+int(v.dataOffset))
	}
	if v, ok := vars["LapDistPct"]; ok && v.varType == varTypeFloat {
		ld.LapDistPct = readFloat32(base, dataBase+int(v.dataOffset))
	}

	// CarIdx arrays — each is indexed by CarIdx. Count is published per-variable
	// in the header (typically 64). We copy into Go slices so callers can use
	// the data after we unmap.
	if v, ok := vars["CarIdxLapDistPct"]; ok && v.varType == varTypeFloat {
		ld.CarIdxLapDistPct = readFloat32Slice(base, dataBase+int(v.dataOffset), int(v.count))
	}
	if v, ok := vars["CarIdxLapCompleted"]; ok && v.varType == varTypeInt {
		ld.CarIdxLapCompleted = readInt32Slice(base, dataBase+int(v.dataOffset), int(v.count))
	}
	if v, ok := vars["CarIdxEstTime"]; ok && v.varType == varTypeFloat {
		ld.CarIdxEstTime = readFloat32Slice(base, dataBase+int(v.dataOffset), int(v.count))
	}
	if v, ok := vars["CarIdxPosition"]; ok && v.varType == varTypeInt {
		ld.CarIdxPosition = readInt32Slice(base, dataBase+int(v.dataOffset), int(v.count))
	}
	if v, ok := vars["CarIdxClassPosition"]; ok && v.varType == varTypeInt {
		ld.CarIdxClassPosition = readInt32Slice(base, dataBase+int(v.dataOffset), int(v.count))
	}

	// Parse session info YAML for track/car names, player CarIdx, and all drivers.
	ld.MyCarIdx = -1
	sessionInfoOff := int(readInt32(base, hdrOffSessionInfoOff))
	sessionInfoLen := int(readInt32(base, hdrOffSessionInfoLen))
	if sessionInfoLen > 0 && sessionInfoLen < maxSessionInfoBytes {
		raw := (*[maxSessionInfoBytes]byte)(unsafe.Add(base, sessionInfoOff))[:sessionInfoLen]
		yaml := strings.TrimRight(string(raw), "\x00")
		ld.Track = yamlField(yaml, "TrackDisplayName")
		ld.Car = yamlField(yaml, "CarScreenName")
		ld.MyCarIdx = DriverCarIdxFromYAML(yaml)
		ld.Drivers = ParseDrivers(yaml)
	}

	return ld
}

// yamlField extracts the value of a simple "Key: Value" line from a YAML string.
// Returns "" if not found. Strips surrounding whitespace and quotes.
// NOTE: internal/analysis/lap.go has a duplicate — keep behaviour in sync.
func yamlField(yaml, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			val := strings.TrimSpace(trimmed[len(prefix):])
			val = strings.Trim(val, "\"'")
			return val
		}
	}
	return ""
}

func readInt32(base unsafe.Pointer, off int) int32 {
	return *(*int32)(unsafe.Add(base, off))
}

func readFloat32(base unsafe.Pointer, off int) float32 {
	return *(*float32)(unsafe.Add(base, off))
}

func readFloat64(base unsafe.Pointer, off int) float64 {
	return *(*float64)(unsafe.Add(base, off))
}

// readFloat32Slice copies `count` float32s starting at `off` into a new Go
// slice. Copying is mandatory: the caller keeps the slice after we unmap the
// shared memory view.
func readFloat32Slice(base unsafe.Pointer, off, count int) []float32 {
	if count <= 0 {
		return nil
	}
	src := unsafe.Slice((*float32)(unsafe.Add(base, off)), count)
	dst := make([]float32, count)
	copy(dst, src)
	return dst
}

// readInt32Slice copies `count` int32s starting at `off` into a new Go slice.
func readInt32Slice(base unsafe.Pointer, off, count int) []int32 {
	if count <= 0 {
		return nil
	}
	src := unsafe.Slice((*int32)(unsafe.Add(base, off)), count)
	dst := make([]int32, count)
	copy(dst, src)
	return dst
}

func itoa(n int32) string { return strconv.Itoa(int(n)) }

func nullTermString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
