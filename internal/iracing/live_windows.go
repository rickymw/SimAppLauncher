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
type LiveData struct {
	Connected   bool
	SessionTime float64
	LapDistPct  float32 // 0.0–1.0 fraction of lap distance from S/F line
	Track       string
	Car         string
	ErrMsg      string // diagnostic; empty on success
}

// iRacing shared memory layout constants.
const (
	memMapName = "Local\\IRSDKMemMap"

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
	varHeaderSize    = 144
	vhOffType        = 0
	vhOffDataOffset  = 4
	vhOffName        = 16 // 32 bytes, null-terminated

	// iRacing variable type codes
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
	modKernel32        = syscall.NewLazyDLL("kernel32.dll")
	procOpenFileMapping = modKernel32.NewProc("OpenFileMappingW")
	procMapViewOfFile  = modKernel32.NewProc("MapViewOfFile")
	procUnmapViewOfFile = modKernel32.NewProc("UnmapViewOfFile")
	procCloseHandle    = modKernel32.NewProc("CloseHandle")
)

type varInfo struct {
	varType    int32
	dataOffset int32
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
		nameBytes := (*[32]byte)(unsafe.Add(base, vhBase+vhOffName))[:]
		name := nullTermString(nameBytes)
		if name != "" {
			vars[name] = varInfo{varType: vType, dataOffset: dataOff}
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

	// Parse session info YAML for track and car names
	sessionInfoOff := int(readInt32(base, hdrOffSessionInfoOff))
	sessionInfoLen := int(readInt32(base, hdrOffSessionInfoLen))
	if sessionInfoLen > 0 && sessionInfoLen < maxSessionInfoBytes {
		raw := (*[maxSessionInfoBytes]byte)(unsafe.Add(base, sessionInfoOff))[:sessionInfoLen]
		yaml := strings.TrimRight(string(raw), "\x00")
		ld.Track = yamlField(yaml, "TrackDisplayName")
		ld.Car = yamlField(yaml, "CarScreenName")
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

func itoa(n int32) string { return strconv.Itoa(int(n)) }

func nullTermString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
