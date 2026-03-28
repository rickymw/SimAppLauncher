# CLAUDE.md — SimAppLauncher

## Project overview
Windows CLI tool (`simapplauncher.exe`) that launches, monitors, and closes sim racing apps in sequence, and analyses iRacing `.ibt` telemetry files. Designed for Stream Deck integration. Four subcommands: `start`, `stop`, `status`, `analyze`. Accepts an optional `-config <path>` flag.

## Build
```powershell
go build -o simapplauncher.exe ./cmd/simapplauncher
```

## Tests
```powershell
# Unit tests (always run these — no real processes or files required)
go test ./...

# Full stack e2e (launches real apps — takes ~20s)
go test -tags e2e -v ./internal/launcher/ -run TestE2E_FullStack -timeout 120s
```

## Usage
```powershell
simapplauncher start                                   # launch all apps in config order
simapplauncher stop                                    # kill all apps
simapplauncher status                                  # print running/stopped state
simapplauncher analyze session.ibt                     # best flying lap from file
simapplauncher analyze -lap 3 session.ibt              # specific lap
simapplauncher analyze -compare 2,3 session.ibt        # side-by-side lap comparison
simapplauncher analyze -update-map session.ibt         # re-detect track segments from this session
simapplauncher analyze -geo-method latlon session.ibt  # detect using GPS curvature instead of lateral G
```

## Architecture

### Key interface
`ProcessManager` in `internal/launcher/launcher.go` abstracts all OS process operations. The Windows implementation is in `process_windows.go`. Tests use a `mockPM` struct with configurable function fields — `spawnFn`, `runningFn`, `killFn`.

### Windows process management
- **Launch**: `os/exec` + `syscall.SysProcAttr{HideWindow: bool}`
- **Status**: `tasklist /FI "IMAGENAME eq name.exe" /NH /FO CSV` — returns `(pid, running, error)`
- **Stop**: `taskkill /F /IM name.exe`, with fallback to `SeDebugPrivilege` + `OpenProcess`/`TerminateProcess` for elevated processes (e.g. SimHub)
- **Elevation**: `ShellExecuteExW` via `shell32.dll` (used when `elevate: true` in config)

### Shared Windows API declarations
`kernel32` is declared in `elevate_windows.go` and shared across all `//go:build windows` files in the `launcher` package. `advapi32` is declared in `process_windows.go`. Both are package-level vars — no redeclaration needed in other files.

### Config
`launcher.config.json` lives next to the binary by default. Override with `-config <path>`. Config is validated on load — rejects empty `name`/`path`, negative `delayMs`, and invalid `windowStyle`.

Top-level fields:
- `driver` — iRacing `UserName` (e.g. `"Ricky Maw"`). Used by `analyze` to match the player's car in multi-class sessions. Case-insensitive. Falls back to `DriverCarIdx` if omitted or no match.
- `apps` — list of apps to launch/stop.

The `processName` field is the exe stem used for `tasklist`/`taskkill`. Falls back to `name` if empty. Must match Task Manager's image name, which may differ from the launched exe if the app spawns a child process.

### `Args` field
`config.App.Args` is a `string`, not `[]string`. Split with `strings.Fields(app.Args)` before passing to `exec.Command`.

### Telemetry analysis (`internal/analysis`)
Parses iRacing `.ibt` binary files and produces per-lap segment statistics.

- **`internal/ibt`** — low-level `.ibt` parser: reads the file header, variable descriptors, and raw sample buffers. `File.Sample(i)` returns a typed accessor for all channels at sample `i`.
- **`internal/analysis/lap.go`** — `ExtractLaps` splits the sample stream into `Lap` objects at S/F crossings (detected by `LapDistPct` dropping > 0.5). Classifies each lap as `flying`, `out lap`, `in lap`, or `out/in lap` based on entry/exit speed. `ParseTrackLength` extracts the track length in metres from the session YAML.
- **`internal/analysis/zones.go`** — `SegmentStats` computes per-segment speed, inputs, G-forces, ABS count, and coasting samples. Uses *effective boundaries*: for corners/chicanes with a stored `BrakeEntryPct`, that value is used as the segment entry (instead of the geometric `EntryPct`), and each preceding straight's exit is clipped to the corner's `BrakeEntryPct` so braking-zone samples are attributed to the corner. `SegmentDeltas` uses the same effective boundaries for timing. `ComputeBrakeEntries(laps, segs)` scans flying laps backward from each corner's geometric entry to find the average braking onset (Brake > 5%), returning a `[]float32` of effective entry percentages. The older fixed `ZoneStats`/`ZoneDeltas` (20 × 5% zones) are retained but not used by the CLI.
- **`internal/trackmap`** — geometry-based corner/straight detection and persistent storage.
- **`cmd/simapplauncher/analyze.go`** — `RunAnalyze` implements the `analyze` subcommand.

#### Out/in lap detection
A lap is flagged as an **out lap** if the first sample's speed < 5 m/s (rolling from pit/grid). It is flagged as an **in lap** if the last sample's speed < 5 m/s (pulling into pit lane). Both together = **out/in lap**. Out/in laps are shown in the lap list but excluded from best-lap selection and not used as comparison targets unless explicitly requested with `-lap N`.

#### Driver/car resolution
`ParseSessionMeta(yaml, driverName)` in `lap.go`:
1. Scans the `Drivers:` list for a block whose `UserName` matches `driverName` (case-insensitive).
2. Falls back to `DriverCarIdx` (the recording driver's own index).
3. Last resort: first `CarScreenName` in the file.

This ensures the correct car is shown in multi-class sessions where many different car types are listed.

### Track segmentation (`internal/trackmap`)

Segments the track into corners and straights using lateral acceleration telemetry. Results are cached in `trackmap.json` next to the binary.

#### Detection pipeline (`detect.go`)
`DetectFromMultiple(allSamples [][]Sample, trackLengthM float64) []Segment` averages the abs(LatAccel) profiles from all provided laps element-wise before running:
1. Bucket into 1000 position bins (0.1% each)
2. Forward-fill empty bins
3. Circular box-smooth over ~15m window
4. Hysteresis classification: enter corner at ≥ 5.0 m/s² (≈0.5G), exit at < 2.5 m/s²
5. Group into raw segments
6. Merge short segments (min 1.2% for straights, 0.6% for corners)
7. Merge chicanes: [corner, short-straight ≤ 1.8%, opposite-direction corner] → single chicane segment
8. Label sequentially: S1, T1, S2, T2, T3-T4 (chicane), etc.

`Detect(samples, trackLengthM)` is a thin wrapper around `DetectFromMultiple` for single-lap use.

`MatchScore(samples, segs)` re-runs the classification pipeline on new samples and checks whether each stored segment boundary has a matching corner/straight transition within ±2% — returns 0.0–1.0.

#### Persistent storage (`trackmap.go`)
`trackmap.json` is a flat JSON map keyed by `TrackDisplayName`. Each entry stores:
- `segments` — the detected segment list
- `trackLengthM` — track length used during detection
- `lapsUsed` / `sessionsUsed` — cumulative counts driving geometry confidence
- `seenSessions` — RFC3339 session start dates; used to deduplicate repeated analysis of the same `.ibt` file
- `detectedFrom` — date of first detection
- `source` — always `"auto"` currently
- `geoMethod` — `"lataccel"` or `"latlon"`; absent on old entries (treated as `"lataccel"`)

Each `Segment` in the list has two boundary representations:
- `entryPct` / `exitPct` — geometric boundaries (where the track physically bends); used for detection, match scoring, and map updates
- `brakeEntryPct` — average lap-distance fraction where drivers begin braking for that corner/chicane; computed from telemetry and blended with a weighted average as more sessions are seen; omitted (zero) for straights and not-yet-computed corners; used by `SegmentStats` and `SegmentDeltas` so that braking-zone samples are correctly attributed to the corner across all comparison laps

Geometry confidence: `low` (< 3 laps), `moderate` (3–10 laps), `high` (> 10 laps).

#### analyze subcommand flow
1. Open `.ibt`, extract session metadata and laps
2. Find best flying lap; collect all flying non-partial-start laps
3. Load `trackmap.json`; if an entry exists for this track use it, else detect from all flying laps and save
4. Compute match score against best lap (always uses lataccel for consistency)
5. On new session or if `brakeEntryPct` is missing from any corner: call `ComputeBrakeEntries`, blend result into stored segments with a weighted average, save
6. Increment `lapsUsed`/`sessionsUsed` once per unique session (keyed by `SessionStartDate`)
7. Print header (Driver / Car / Track / Samples / Map confidence line)
8. Print lap list
9. Print segment table or comparison table; `Entry → Exit` columns show effective (braking-adjusted) boundaries

`-update-map` forces re-detection from the current session regardless of existing data.
`-geo-method latlon` uses GPS curvature for detection instead of lateral G; falls back to `lataccel` with a warning if `Lat`/`Lon` channels are not present in the file. The method used is stored in `trackmap.json` and shown in the Map: output line.

## Deployment
- Binary + config live in `G:\RACING\SimAppLauncher\` (the repo root)
- `trackmap.json` is created automatically on first `analyze` run and lives alongside the binary
- Stream Deck triggers via the **Open** action pointing directly at `G:\RACING\SimAppLauncher\simapplauncher.exe` with arguments `start` or `stop` — no PowerShell wrapper needed. Config path resolves relative to the exe via `os.Executable()`.
- UAC is set to never-notify on this machine — elevation via `ShellExecuteExW runas` does not work in this environment; use `elevate: false` for all apps
- SimHub auto-elevates via its own manifest and resists `taskkill` — the `SeDebugPrivilege` fallback in `Kill()` handles this

## Known limitations
- `Minimized` window style not implemented (requires `golang.org/x/sys/windows` for `StartupInfo`; currently treated as `Normal`)
- `stop` kills by image name — affects all instances of a process if multiple are running
- `processName` whitespace is not trimmed — accidental spaces will cause silent match failures
- `SegmentDeltas` requires both laps to have monotonically increasing `LapDistPct`; laps with backward tracking (e.g. short-cuts) may produce incorrect deltas
- Segment detection with `lataccel` method only uses lateral G — pure braking zones with no lateral load appear as straights (use `-geo-method latlon` to fix this)
- S/F line wraparound: if the first and last segments are both straights they are not automatically merged into one

## Open improvements
- Exit codes: `RunStart`/`RunStop` currently always exit 0 even on partial failures
- CSV parsing in `IsRunning` and `parsePIDFromTasklist` is naive — works because PID is always field[1], but would break if Windows changes the column order
- Segment names are auto-labelled T1/S1/etc — no way to assign real corner names without hand-editing `trackmap.json`
- Same-direction corner complexes (e.g. Maggotts/Becketts) are not merged; only direction-reversing chicanes are detected
