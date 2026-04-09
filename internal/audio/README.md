# internal/audio

Microphone recording via the Windows WinMM API.

## What it does

Records PCM audio from the default microphone for the duration a hotkey is held, then wraps the captured bytes in a RIFF/WAVE container ready to write to disk or pass to Whisper.

## How it works

`Recorder` wraps the WinMM `waveIn*` API:

1. `Start()` — opens the default audio device (`WAVE_MAPPER`) at 16 kHz / 16-bit / mono, prepares a single 60-second pre-allocated buffer (`waveInPrepareHeader`), registers it (`waveInAddBuffer`), and starts capture (`waveInStart`).
2. `Stop()` — calls `waveInStop` + `waveInReset` to flush the buffer, then reads `DwBytesRecorded` to determine how many bytes were captured. Returns a trimmed copy of the PCM data.
3. `BuildWAV(pcm)` — wraps the raw PCM bytes in a standard 44-byte RIFF/WAVE header so the output is a valid `.wav` file.

Format: 16 kHz, 16-bit, mono — matches Whisper's optimal input format and keeps files small (~32 KB/s).

## Architecture

| Symbol | Description |
|---|---|
| `Recorder` | Holds WinMM handle, pre-allocated buffer, and `WAVEHDR`. |
| `Recorder.Start()` | Open device and begin capture. |
| `Recorder.Stop() ([]byte, error)` | Stop capture and return PCM bytes. |
| `BuildWAV(pcm []byte) []byte` | Wrap PCM in RIFF/WAVE container. |

Windows-only (`//go:build windows`). Maximum recording time is 60 seconds; longer recordings are silently truncated at the buffer boundary.
