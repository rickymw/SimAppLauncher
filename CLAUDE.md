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
simapplauncher start                                     # launch all apps in config order
simapplauncher stop                                      # kill all apps
simapplauncher status                                    # print running/stopped state
simapplauncher analyze                                   # analyze most recently modified .ibt in ibtDir
simapplauncher analyze 2                                 # analyze 2nd most recent .ibt in ibtDir
simapplauncher analyze session.ibt                       # analyze specific file
simapplauncher analyze -lap 3 session.ibt                # specific lap
simapplauncher analyze -compare 2,3 session.ibt          # side-by-side lap comparison
simapplauncher analyze -update-map session.ibt           # re-detect track segments from this session
simapplauncher analyze -geo-method lataccel session.ibt  # use lateral G instead of GPS curvature
```

## AI Coaching workflow
When the user asks to be coached, to analyse their session, or to review a lap, use Bash to run the analyze command — do not ask them to paste output.

1. Run `.\simapplauncher.exe analyze` (or with a specific `.ibt` path) to get the lap list
2. Identify the **best lap** ("Selecting best lap: Lap N") and the **most recent flying lap** (highest-numbered flying lap that isn't the best)
3. Run `.\simapplauncher.exe analyze -compare <most-recent-flying>,<best>` to get the segment comparison
4. Read `coach.md` (repo root) for the full coaching framework, column reference, and output format
5. Deliver per-segment findings and a **Top 3 Actions** list

If the user specifies a file or particular lap numbers, pass those through. If there is only one flying lap, skip step 3 and analyse the single-lap output instead.

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

### Config (`launcher.config.json`)
Lives next to the binary by default. Override with `-config <path>`. Config is validated on load — rejects empty `name`/`path`, negative `delayMs`, and invalid `windowStyle`.

Top-level fields:
- `driver` — iRacing `UserName` (e.g. `"Ricky Maw"`). Used by `analyze` to match the player's car in multi-class sessions. Case-insensitive. Falls back to `DriverCarIdx` if omitted or no match.
- `ibtDir` — directory scanned for `.ibt` files when no path is passed to `analyze` (e.g. `"C:\\Users\\ricky\\Documents\\iRacing\\telemetry"`). A bare integer argument (e.g. `analyze 2`) selects the Nth most-recently-modified file; omitting the argument selects the most recent (equivalent to `analyze 1`). Error if N exceeds the file count.
- `apps` — list of apps to launch/stop.

The `processName` field is the exe stem used for `tasklist`/`taskkill`. Falls back to `name` if empty. Must match Task Manager's image name, which may differ from the launched exe if the app spawns a child process.

### `Args` field
`config.App.Args` is a `string`, not `[]string`. Split with `strings.Fields(app.Args)` before passing to `exec.Command`.

### Telemetry analysis (`internal/analysis`)
Parses iRacing `.ibt` binary files and produces per-lap segment statistics.

- **`internal/ibt`** — low-level `.ibt` parser: reads the file header, variable descriptors, and raw sample buffers. `File.Sample(i)` returns a typed accessor for all channels at sample `i`. `File.DiskHeader().SessionStartDate` is a `time.Time` stored as UTC (use `.Local()` for display).
- **`internal/analysis/lap.go`** — `ExtractLaps` splits the sample stream into `Lap` objects at S/F crossings (detected by `LapDistPct` dropping > 0.5). Classifies each lap as `flying`, `out lap`, `in lap`, or `out/in lap` based on entry/exit speed. `ParseTrackLength` extracts the track length in metres from the session YAML. `ParseWeather` extracts air and track temperatures from the session YAML (e.g. `"Air 27°C, Track 40°C"`); uses `AirTemp` and `TrackTemp` fields, falls back to `TrackSurfaceTemp`.
- **`internal/analysis/zones.go`** — `SegmentStats` computes per-segment speed, inputs, G-forces, ABS count, and coasting samples. Uses *effective boundaries*: for corners/chicanes with a stored `BrakeEntryPct`, that value is used as the segment entry (instead of the geometric `EntryPct`), and each preceding straight's exit is clipped to the corner's `BrakeEntryPct` so braking-zone samples are attributed to the corner. `SegmentDeltas` uses the same effective boundaries for timing. `ComputeBrakeEntries(laps, segs)` scans flying laps backward from each corner's geometric entry to find the average braking onset (Brake > 5%), returning a `[]float32` of effective entry percentages. The older fixed `ZoneStats`/`ZoneDeltas` (20 × 5% zones) are retained but not used by the CLI. Key `SegZone` fields: `LatGAvg` (average, not peak), `CoastSamples` (raw count; display as `CoastSamples/60` seconds).
- **`internal/trackmap`** — geometry-based corner/straight detection and persistent storage.
- **`internal/pb`** — personal-best tracking; see below.
- **`cmd/simapplauncher/analyze.go`** — `RunAnalyze(args, cfg, trackmapPath, pbPath)` implements the `analyze` subcommand.

#### Out/in lap detection
A lap is flagged as an **out lap** if the first sample's speed < 5 m/s (rolling from pit/grid). It is flagged as an **in lap** if the last sample's speed < 5 m/s (pulling into pit lane). Both together = **out/in lap**. Out/in laps are shown in the lap list but excluded from best-lap selection and not used as comparison targets unless explicitly requested with `-lap N`.

#### Driver/car resolution
`ParseSessionMeta(yaml, driverName)` in `lap.go`:
1. Scans the `Drivers:` list for a block whose `UserName` matches `driverName` (case-insensitive).
2. Falls back to `DriverCarIdx` (the recording driver's own index).
3. Last resort: first `CarScreenName` in the file.

This ensures the correct car is shown in multi-class sessions where many different car types are listed.

### Track segmentation (`internal/trackmap`)

Segments the track into corners and straights. Results are cached in `trackmap.json` next to the binary. Default detection method is `latlon` (GPS curvature); `lataccel` (lateral G) is available via `-geo-method lataccel`.

#### Detection pipeline (`detect.go`)

**`lataccel` method** — `DetectFromMultiple(allSamples [][]Sample, trackLengthM float64) []Segment` averages the abs(LatAccel) profiles from all provided laps element-wise before running:
1. Bucket into 1000 position bins (0.1% each)
2. Forward-fill empty bins
3. Circular box-smooth over ~15m window
4. Hysteresis classification: enter corner at ≥ 5.0 m/s² (≈0.5G), exit at < 2.5 m/s²
5. Group into raw segments
6. Merge short segments (min 1.2% for straights, 0.6% for corners)
7. Merge chicanes: [corner, short-straight ≤ 1.8%, opposite-direction corner] → single chicane segment
8. Label sequentially: S1, T1, S2, T2, T3-T4 (chicane), etc.

**`latlon` method** — `DetectFromMultipleLatLon(allSamples [][]Sample, trackLengthM float64) []Segment`:
1. Compute shared equirectangular projection origin (mean lat/lon across all samples)
2. Accumulate bin-averaged XY positions across all laps (reduces random GPS noise by √N)
3. Forward-fill empty bins
4. Compute signed curvature on binned positions using triplets spaced ~20m apart
5. Feed absolute curvature profile into the same `detectFromProfiles` pipeline with thresholds κ ≥ 0.004 m⁻¹ (enter) / < 0.0015 m⁻¹ (exit)
6. Returns `nil` if `Lat`/`Lon` channels absent — caller falls back to lataccel with a warning

Note: GPS quantisation in iRacing is ~0.1m; per-sample noise is systematic (same rounding each lap) so averaging more laps does not reduce it — bin-averaging and wide triplet spacing are the mitigations.

`Detect(samples, trackLengthM)` is a thin wrapper around `DetectFromMultiple` for single-lap use.

`MatchScore(samples, segs)` re-runs the lataccel classification pipeline on new samples and checks whether each stored segment boundary has a matching corner/straight transition within ±2% — returns 0.0–1.0. Always uses lataccel for consistency regardless of stored `geoMethod`.

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

### Personal best tracking (`internal/pb`)
`pb.go` loads and saves `pb.json` next to the binary. Structure: flat JSON map keyed by `"Car|Track"` → `PersonalBest{LapTime, LapTimeFormatted, Date, Weather, Car, Track}`. `pb.Update(pbf, car, track, lapTime, formatted, date, weather)` returns `true` if a new PB was set. Weather is populated by `ParseWeather` (air/track temps). Date uses `.Local()` on `SessionStartDate` so it matches the session filename.

### analyze subcommand flow
1. Resolve `.ibt` path: explicit path, numeric index into `ibtDir`, or most-recent from `ibtDir`
2. Open `.ibt`, extract session metadata and laps
3. Find best flying lap; collect all flying non-partial-start laps
4. Load `trackmap.json`; if an entry exists for this track use it, else detect from all flying laps and save
5. Compute match score against best lap (always uses lataccel for consistency)
6. On new session or if `brakeEntryPct` is missing from any corner: call `ComputeBrakeEntries`, blend result into stored segments with a weighted average, save
7. Increment `lapsUsed`/`sessionsUsed` once per unique session (keyed by `SessionStartDate`)
8. Print header: File (if auto-selected) / Driver / Car / Track / Samples / Map line
9. Load `pb.json`; compare best lap against stored PB; update and save if new PB; print PB line
10. Print lap list (time and kind only — no sample count)
11. Print segment table or comparison table

`-update-map` forces re-detection from the current session regardless of existing data.
`-geo-method` controls segment detection: `latlon` (default, GPS curvature) or `lataccel` (lateral G). `latlon` falls back to `lataccel` with a warning if `Lat`/`Lon` channels are not present in the file. The method used is stored in `trackmap.json` and shown in the Map: output line.

### Segment table columns
`EntSpd | MinSpd | ExtSpd | Gear | Brk% | PkBrk | FThr% | AvgLatG | ABS | Coast`
- Speeds in km/h. Gear = max for straights, min for corners/chicanes. Brk% = fraction of samples with brake > 2%. PkBrk = peak brake pressure (0–100%). FThr% = fraction at full throttle (> 95%). AvgLatG = mean abs(LatAccel)/9.81 over the segment. ABS = sample count with ABS active. Coast = seconds with throttle < 5% AND brake < 5% (CoastSamples / 60).

## Runtime files
All live next to the binary in `G:\RACING\SimAppLauncher\`:
| File | Created by | Purpose |
|------|-----------|---------|
| `launcher.config.json` | hand-edited | app list, driver name, ibtDir |
| `trackmap.json` | auto on first `analyze` | segment geometry per track |
| `pb.json` | auto on first `analyze` | personal best per car/track |

## Deployment
- Binary + config live in `G:\RACING\SimAppLauncher\` (the repo root)
- Stream Deck triggers via the **Open** action pointing directly at `G:\RACING\SimAppLauncher\simapplauncher.exe` with arguments `start` or `stop` — no PowerShell wrapper needed. Config path resolves relative to the exe via `os.Executable()`.
- UAC is set to never-notify on this machine — elevation via `ShellExecuteExW runas` does not work in this environment; use `elevate: false` for all apps
- SimHub auto-elevates via its own manifest and resists `taskkill` — the `SeDebugPrivilege` fallback in `Kill()` handles this

## Known limitations
- `Minimized` window style not implemented (requires `golang.org/x/sys/windows` for `StartupInfo`; currently treated as `Normal`)
- `stop` kills by image name — affects all instances of a process if multiple are running
- `processName` whitespace is not trimmed — accidental spaces will cause silent match failures
- `SegmentDeltas` requires both laps to have monotonically increasing `LapDistPct`; laps with backward tracking (e.g. short-cuts) may produce incorrect deltas
- Segment detection with `lataccel` method only uses lateral G — pure braking zones with no lateral load appear as straights (`latlon` default avoids this)
- S/F line wraparound: if the first and last segments are both straights they are not automatically merged into one
- GPS quantisation in iRacing is systematic (same rounding each lap) so averaging more laps does not reduce noise in the `latlon` method — mitigated by bin-averaging and wide triplet spacing but not eliminated
- Dynamic weather sessions do not populate `AirTemp` in the session YAML; PB weather shows track temp only in that case
- `pb.json` is never pruned — old car/track combos accumulate indefinitely

## Open improvements
- Exit codes: `RunStart`/`RunStop` currently always exit 0 even on partial failures
- CSV parsing in `IsRunning` and `parsePIDFromTasklist` is naive — works because PID is always field[1], but would break if Windows changes the column order
- Segment names are auto-labelled T1/S1/etc — no way to assign real corner names without hand-editing `trackmap.json`
- Same-direction corner complexes (e.g. Maggotts/Becketts) are not merged; only direction-reversing chicanes are detected
- `latlon` geo-method could be improved by using `VelocityX`/`VelocityY` channels (world-frame velocity) to compute heading-change rate instead of GPS curvature — avoids GPS quantisation entirely and should give a cleaner curvature proxy than bin-averaged lat/lon positions
- Sector times: group segments into logical sectors and show sector time per lap, so the coachable third of the track is immediately visible without running `-compare`
- Exit speed vs straight entry speed: surface the direct relationship between corner exit speed and the subsequent straight's max speed — the primary measure of whether a corner exit is costing time on the following straight
- AI coaching via `-coach` flag: send the segment table, lap list, PB delta, and lap time trend to the Anthropic API and print actionable coaching feedback. Input is ~700 tokens (the existing analyze output as-is). Requires `ANTHROPIC_API_KEY` env var. Use `claude-haiku` for cost (~$0.001 per call).
