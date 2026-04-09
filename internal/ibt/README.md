# internal/ibt

Low-level parser for iRacing `.ibt` binary telemetry files.

## What it does

Opens an `.ibt` file, reads all header and variable metadata up front, then provides random-access to individual sample rows. Each sample exposes typed accessors (`Float32`, `Float64`, `Int`, `Bool`) keyed by iRacing channel name (e.g. `"Speed"`, `"LapDistPct"`).

## How it works

An `.ibt` file has three header sections at fixed offsets, followed by a flat array of fixed-width data rows:

```
Offset 0   → irsdk_header (112 bytes)       — tick rate, variable count, buffer layout
Offset 112 → irsdk_diskSubHeader (32 bytes) — session start date, lap/record counts
Offset H.VarHeaderOffset → N × 144-byte variable headers (name, type, offset within row)
Offset H.VarBuf[0].BufOffset → data rows (H.BufLen bytes each, H.SessionRecordCount rows)
```

`Open` reads all three header sections and builds a `map[string]*VarDef` name index. Variable data is _not_ read at open time. `Sample(n)` seeks to `DataOffset + n*BufLen` and reads exactly one row via `ReadAt`, making it safe to call concurrently from multiple goroutines.

Sanity bounds are enforced at parse time (max 10 MB session YAML, max 4096 variables, each variable's byte range must fit within the row width) to guard against corrupt files.

## Architecture

| Symbol | Description |
|---|---|
| `File` | Holds the open `*os.File` and all parsed metadata. Call `Close()` when done. |
| `Header` | Parsed `irsdk_header`: tick rate, variable count, data offset. |
| `DiskHeader` | Parsed `irsdk_diskSubHeader`: `SessionStartDate` (UTC `time.Time`), record count. |
| `VarDef` | One telemetry channel: name, type, byte offset, array count. |
| `Sample` | One data row; typed accessors `Float32(name)`, `Float64(name)`, `Int(name)`, `Bool(name)`. |
| `VarType` | iRacing type constants (`VarChar`, `VarBool`, `VarInt`, `VarFloat`, `VarDouble`). |

### Key functions

```go
f, err := ibt.Open("session.ibt")
defer f.Close()

n := f.NumSamples()         // total data rows
s, err := f.Sample(i)       // read row i (0-based)
speed, ok := s.Float32("Speed")  // typed channel access
```

`DiskHeader().SessionStartDate` is stored as UTC; call `.Local()` for local-time display.
