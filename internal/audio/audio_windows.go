//go:build windows

// Package audio provides microphone recording via the Windows WinMM API.
package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	sampleRate    = 16000
	bitsPerSample = 16
	numChannels   = 1
	maxRecordSecs = 60

	// Derived audio format constants — all determined at compile time.
	blockAlign     = numChannels * (bitsPerSample / 8)              // 2 bytes per sample
	avgBytesPerSec = sampleRate * blockAlign                        // 32000 bytes/s
	bufferBytes    = avgBytesPerSec * maxRecordSecs                 // 1,920,000

	waveMapper    = 0xFFFFFFFF
	waveFmtPCM    = 1
	callbackNull  = 0
	mmSysErrNoErr = 0
)

var (
	modWinMM               = syscall.NewLazyDLL("winmm.dll")
	procWaveInOpen         = modWinMM.NewProc("waveInOpen")
	procWaveInClose        = modWinMM.NewProc("waveInClose")
	procWaveInPrepHeader   = modWinMM.NewProc("waveInPrepareHeader")
	procWaveInAddBuffer    = modWinMM.NewProc("waveInAddBuffer")
	procWaveInStart        = modWinMM.NewProc("waveInStart")
	procWaveInStop         = modWinMM.NewProc("waveInStop")
	procWaveInReset        = modWinMM.NewProc("waveInReset")
	procWaveInUnprepHeader = modWinMM.NewProc("waveInUnprepareHeader")
)

// waveFormatEx mirrors WAVEFORMATEX. CbSize is present but set to 0;
// PCM format does not use the extension bytes.
type waveFormatEx struct {
	WFormatTag      uint16
	NChannels       uint16
	NSamplesPerSec  uint32
	NAvgBytesPerSec uint32
	NBlockAlign     uint16
	WBitsPerSample  uint16
	CbSize          uint16
}

// waveHdr mirrors WAVEHDR.
type waveHdr struct {
	LpData          uintptr
	DwBufferLength  uint32
	DwBytesRecorded uint32
	DwUser          uintptr
	DwFlags         uint32
	DwLoops         uint32
	LpNext          uintptr
	Reserved        uintptr
}

// Recorder records PCM audio from the default microphone.
type Recorder struct {
	handle uintptr
	buf    []byte
	hdr    waveHdr
}

// Start opens the default microphone and begins recording.
func (r *Recorder) Start() error {
	r.buf = make([]byte, bufferBytes)

	wfx := waveFormatEx{
		WFormatTag:      waveFmtPCM,
		NChannels:       numChannels,
		NSamplesPerSec:  sampleRate,
		NAvgBytesPerSec: avgBytesPerSec,
		NBlockAlign:     blockAlign,
		WBitsPerSample:  bitsPerSample,
		CbSize:          0,
	}

	ret, _, _ := procWaveInOpen.Call(
		uintptr(unsafe.Pointer(&r.handle)),
		uintptr(waveMapper),
		uintptr(unsafe.Pointer(&wfx)),
		0, 0, callbackNull,
	)
	if ret != mmSysErrNoErr {
		return fmt2err("waveInOpen", ret)
	}

	r.hdr = waveHdr{
		LpData:         uintptr(unsafe.Pointer(&r.buf[0])),
		DwBufferLength: bufferBytes,
	}

	ret, _, _ = procWaveInPrepHeader.Call(
		r.handle,
		uintptr(unsafe.Pointer(&r.hdr)),
		unsafe.Sizeof(r.hdr),
	)
	if ret != mmSysErrNoErr {
		procWaveInClose.Call(r.handle)
		return fmt2err("waveInPrepareHeader", ret)
	}

	ret, _, _ = procWaveInAddBuffer.Call(
		r.handle,
		uintptr(unsafe.Pointer(&r.hdr)),
		unsafe.Sizeof(r.hdr),
	)
	if ret != mmSysErrNoErr {
		procWaveInUnprepHeader.Call(r.handle, uintptr(unsafe.Pointer(&r.hdr)), unsafe.Sizeof(r.hdr))
		procWaveInClose.Call(r.handle)
		return fmt2err("waveInAddBuffer", ret)
	}

	ret, _, _ = procWaveInStart.Call(r.handle)
	if ret != mmSysErrNoErr {
		procWaveInReset.Call(r.handle)
		procWaveInUnprepHeader.Call(r.handle, uintptr(unsafe.Pointer(&r.hdr)), unsafe.Sizeof(r.hdr))
		procWaveInClose.Call(r.handle)
		return fmt2err("waveInStart", ret)
	}

	return nil
}

// Stop halts recording and returns the captured PCM bytes.
func (r *Recorder) Stop() ([]byte, error) {
	procWaveInStop.Call(r.handle)
	procWaveInReset.Call(r.handle)
	procWaveInUnprepHeader.Call(r.handle, uintptr(unsafe.Pointer(&r.hdr)), unsafe.Sizeof(r.hdr))
	procWaveInClose.Call(r.handle)

	recorded := int(r.hdr.DwBytesRecorded)
	if recorded == 0 {
		return nil, fmt.Errorf("no audio recorded")
	}
	pcm := make([]byte, recorded)
	copy(pcm, r.buf[:recorded])
	return pcm, nil
}

// BuildWAV wraps raw PCM bytes in a minimal RIFF/WAVE container.
// The PCM must be 16kHz, 16-bit, mono (matching the Recorder format).
func BuildWAV(pcm []byte) []byte {
	const headerSize = 44
	var buf bytes.Buffer
	buf.Grow(headerSize + len(pcm))

	// RIFF chunk
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")

	// fmt sub-chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16)) // PCM fmt chunk is always 16 bytes
	binary.Write(&buf, binary.LittleEndian, uint16(waveFmtPCM))
	binary.Write(&buf, binary.LittleEndian, uint16(numChannels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(avgBytesPerSec))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample))

	// data sub-chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)

	return buf.Bytes()
}

func fmt2err(fn string, code uintptr) error {
	return fmt.Errorf("audio: %s returned error code %d", fn, code)
}
