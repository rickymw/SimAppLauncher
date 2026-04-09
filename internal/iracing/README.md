# internal/iracing

Reads a live telemetry snapshot from iRacing's shared memory interface.

## What it does

Returns a `LiveData` struct with the current session time, lap distance fraction, track name, and car name. Used by the `notes` subcommand to stamp each voice note with the driver's track position at the moment of key-release.

## How it works

iRacing exposes a named shared memory segment (`Local\IRSDKMemMap`) that mirrors its in-memory data in real time. `ReadLiveData`:

1. Opens the mapping with `OpenFileMappingW` / `MapViewOfFile`
2. Reads `irsdk_header` status field — returns `Connected=false` if iRacing isn't in a live session
3. Builds a `map[string]varInfo` from the variable header array (type + data offset per channel)
4. Finds the most-recent data buffer (highest `tickCount` among the four rolling buffers)
5. Reads `SessionTime` (float64) and `LapDistPct` (float32) from that buffer
6. Parses `TrackDisplayName` and `CarScreenName` from the session info YAML embedded in shared memory

All memory access is via `unsafe.Pointer` casts to avoid an extra copy — the mapped region is read-only.

## Architecture

| Symbol | Description |
|---|---|
| `LiveData` | Snapshot: `Connected`, `SessionTime`, `LapDistPct`, `Track`, `Car`, `ErrMsg`. |
| `ReadLiveData()` | Single entry point; returns zero `LiveData` if iRacing is not running. |

`ErrMsg` is set (but `Connected` remains false) when the call fails for a diagnosable reason (e.g. `OpenFileMappingW` error) so callers can distinguish "not running" from "unexpected failure".

Windows-only (`//go:build windows`).
