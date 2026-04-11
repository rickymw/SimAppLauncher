# CLAUDE.md — MotorHome

## Project overview
Windows CLI tool (`motorhome.exe`) that launches, monitors, and closes sim racing apps in sequence, analyses iRacing `.ibt` telemetry files, and records voice notes during a session. Designed for Stream Deck integration. Five subcommands: `start`, `stop`, `status`, `analyze`, `notes`. Accepts an optional `-config <path>` flag.

## Documentation rule
When making any code change, always review and update documentation to match:
- The **package README** for any package you modified
- **CLAUDE.md** if the change affects architecture, data flow, config fields, subcommand behaviour, or known limitations
- **README.md** if the change affects user-facing behaviour, usage examples, or the package overview table

Documentation must be updated in the same pass as the code — never left as a follow-up.

## Testing rule
When making any code change, always create or update tests to match:
- Add tests for any new function or behaviour
- Update existing tests if the change alters expected inputs, outputs, or side effects
- Run `go test ./...` before considering the change complete

Tests must be written in the same pass as the code — never left as a follow-up.

## Build
```powershell
go build -o motorhome.exe ./cmd/motorhome
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
motorhome start                                     # launch all apps in config order
motorhome stop                                      # kill all apps
motorhome status                                    # print running/stopped state
motorhome analyze                                   # analyze most recently modified .ibt in ibtDir
motorhome analyze 2                                 # analyze 2nd most recent .ibt in ibtDir
motorhome analyze session.ibt                       # analyze specific file
motorhome analyze -lap 3 session.ibt                # specific lap
motorhome analyze -update-map session.ibt           # re-detect track segments from this session
motorhome analyze -geo-method lataccel session.ibt  # use lateral G instead of GPS curvature
motorhome analyze -dump T3 session.ibt              # dump T3 telemetry to CSV for AI analysis
motorhome analyze -dump 5 -lap 3 session.ibt        # dump 5th segment from lap 3
```

## AI Coaching workflow
When the user asks to be coached, to analyse their session, or to review a lap, use Bash to run the analyze command — do not ask them to paste output.

1. Run `.\motorhome.exe analyze` (or with a specific `.ibt` path) to get the phase table for the best lap
2. Read `coach.md` (repo root) for the full coaching framework, column reference, and output format
3. Deliver per-segment findings using entry/mid/exit phase data and a **Top 3 Actions** list

If the user specifies a file or particular lap numbers, pass those through.

## Architecture

Each package has its own README with full detail. Below is a terse summary with links.

| Package | Role | Details |
|---|---|---|
| `cmd/motorhome` | Entry point, flag parsing, subcommand dispatch (`analyze.go`, `notes.go`) | [README](cmd/motorhome/README.md) |
| `internal/config` | `Config`/`App` structs, JSON load, `Validate()` | [README](internal/config/README.md) |
| `internal/launcher` | `ProcessManager` interface; `RunStart`/`RunStop`/`RunStatus`; `tasklist`/`taskkill`; `SeDebugPrivilege` fallback | [README](internal/launcher/README.md) |
| `internal/ibt` | Low-level `.ibt` binary parser; `File.Sample(i)` typed accessor | [README](internal/ibt/README.md) |
| `internal/analysis` | `ExtractLaps`, `ComputePhases`, `ComputeBrakeEntries`, `ComputeTyreSummary`, `DumpSegmentCSV`, `ParseSessionMeta` | [README](internal/analysis/README.md) |
| `internal/trackmap` | GPS curvature corner detection (`latlon`) with steering/speed/lat-G validation; fallback `lataccel`; `trackmap.json` load/save | [README](internal/trackmap/README.md) |
| `internal/pb` | Personal best store; `pb.Update` returns true on new PB | [README](internal/pb/README.md) |
| `internal/notes` | `Note{Timestamp,Text}`/`Session` types; `AppendNote` load→append→save | [README](internal/notes/README.md) |
| `internal/iracing` | `ReadLiveData()` snapshot from iRacing shared memory (currently unused) | [README](internal/iracing/README.md) |
| `internal/audio` | WinMM `Recorder.Start/Stop`; `BuildWAV` for Whisper input | [README](internal/audio/README.md) |

### Config (`launcher.config.json`)
Lives next to the binary by default. Override with `-config <path>`. Validated on load — rejects empty `name`/`path`, negative `delayMs`, invalid `windowStyle`.

Key top-level fields:
- `driver` — iRacing `UserName`; used by `analyze` to match the player's car in multi-class sessions. Case-insensitive; falls back to `DriverCarIdx`.
- `ibtDir` — directory scanned for `.ibt` files when no path is passed to `analyze`. Bare integer arg selects the Nth most-recent file.
- `hotkey` — key name for voice notes (set via `notes set-hotkey`).
- `whisperPath` / `whisperModel` — paths to `whisper-cli.exe` and model file.
- `apps[].processName` — exe stem for `tasklist`/`taskkill`; falls back to `name`. Must match Task Manager's image name.
- `apps[].args` — `string`, not `[]string`; split with `strings.Fields` before passing to `exec.Command`.

### analyze subcommand flow (`cmd/motorhome/analyze.go`)
1. Resolve `.ibt` path: explicit, numeric index into `ibtDir`, or most-recent
2. Open `.ibt`; extract session metadata and laps
3. Find best flying lap; filter flying laps to within 1.5s of best lap time (drops slow early-practice laps)
4. Load `trackmap.json`; detect from filtered laps if no entry exists (latlon → lataccel fallback)
5. Compute match score (always lataccel for consistency); compute/blend `brakeEntryPct` on new sessions using filtered laps
6. Increment `lapsUsed`/`sessionsUsed` once per unique session; save trackmap
7. Load `pb.json`; update if new PB; save
8. Print: header (file, driver, car, track) → setup tables (Tyres + Suspension corners parsed from CarSetup YAML) → tyre summary (avg carcass temps, end-of-lap wear, hot pressures, brake bias) → map line → PB line → lap list → phase table

`-update-map` forces re-detection. `-geo-method latlon|lataccel` selects detection method. `-dump <segment>` writes a downsampled (20Hz) CSV of the segment's telemetry for AI analysis — accepts segment name (T3) or 1-based index (3). Output includes 1s of context before/after.

### notes subcommand flow (`cmd/motorhome/notes.go`)
Toggle model — each press starts or stops recording:
1. First press → play start chime (A5→C6 via `kernel32.Beep`), start `audio.Recorder`
2. Second press → stop `Recorder`, play stop chime (E5→A4), transcribe via `whisper-cli`
3. Append `Note{Timestamp, Text}` to session JSON file; print `[note] transcribed text`

`notes set-hotkey` installs a keyboard hook and Raw Input listener simultaneously; first input wins and is saved to config. HID button-release events are discarded (toggle only cares about press).

### Phase table columns
`Name | Phase | Spd (entry→exit km/h) | OnBrk | PkBrk | Thr% | LatG | Wheel° | Corr | ABS | Lock | Spin | Coast`
— Phase = entry/mid/exit/full. Straights get one "full" phase. Corners are split into entry/mid/exit using 80% of peak |SteeringAngle| as the commitment threshold. Corners with peak steering < 5° get a single "full" phase. Spd = entry and exit speed in km/h. OnBrk = % of phase time with brake applied (>2%). PkBrk = peak brake pressure. Thr% = samples at full throttle > 95%. LatG = mean abs(LatAccel)/9.81. Wheel° = peak absolute steering wheel angle in the phase (degrees; steering wheel, not road wheel — divide by steering ratio for tyre angle). Corr = steering direction reversals above threshold within the phase. ABS = samples with ABS active. Lock = samples where any wheel speed < 95% of vehicle speed under braking. Spin = samples where any wheel speed > 105% of vehicle speed under power. Coast = seconds (CoastSamples / 60).

### Telemetry channels extracted
SampleData extracts ~60 channels from .ibt files: core timing/position (LapDistPct, SessionTime, Speed, Lat, Lon), driver inputs processed and raw (Throttle/ThrottleRaw, Brake/BrakeRaw, Clutch, Gear, SteeringAngle), engine (RPM), vehicle dynamics (LongAccel, LatAccel, YawRate), driver aids (ABSActive, ABSCutPct, BrakeBias, TCSetting, ABSSetting), wheel speeds (LF/RF/LR/RR), tyre carcass temps (4×3 CL/CM/CR), tyre wear (4×3 L/M/R), tyre pressures (4), brake line pressures (4), fuel (FuelLevel, FuelUsePerHour), and steering feedback (SteeringWheelTorque). Missing channels default to zero.

### Lap timing
`LapLastLapTime` is read from the S/F crossing frame and used as the authoritative lap time (matches iRacing UI and tools like Garage61). Falls back to `SessionTime[last] − SessionTime[first]` if the channel is absent.

### Out/in lap detection
Out lap: first sample speed < 5 m/s. In lap: last sample speed < 5 m/s. Shown in lap list; excluded from best-lap selection unless forced with `-lap N`.

### Driver/car resolution
`ParseSessionMeta(yaml, driverName)`: match `UserName` case-insensitively → fallback `DriverCarIdx` → first `CarScreenName`.

## Adding a new subcommand

1. **Business logic** — add a new package `internal/<name>/` with its own `README.md`
2. **Handler** — add `cmd/motorhome/<name>.go` with a `Run<Name>(args, cfg, ...) ` entry point
3. **Wire up** — add a `case "<name>":` in `cmd/motorhome/main.go`; resolve any runtime file paths from `filepath.Dir(*cfgPath)` alongside the existing paths
4. **Config** — if new fields are needed, add them to `internal/config/config.go` and `Config.Validate()`
5. **Usage string** — add the subcommand to `flag.Usage` in `main.go`
6. **Docs** — add a row to the package table in this file and in `README.md`; add usage example to the Usage section in `README.md`

## Runtime files
All live next to the binary in `G:\RACING\SimAppLauncher\`:
| File | Created by | Purpose |
|------|-----------|---------|
| `launcher.config.json` | hand-edited | app list, driver name, ibtDir |
| `trackmap.json` | auto on first `analyze` | segment geometry per track |
| `trackref.json` | hand-edited | expected corner counts per track (guides detection) |
| `pb.json` | auto on first `analyze` | personal best per car/track |

## Deployment
- Binary + config live in `G:\RACING\SimAppLauncher\` (the repo root)
- Stream Deck triggers via the **Open** action pointing directly at `G:\RACING\SimAppLauncher\motorhome.exe` with arguments `start` or `stop` — no PowerShell wrapper needed. Config path resolves relative to the exe via `os.Executable()`.
- UAC is set to never-notify on this machine — elevation via `ShellExecuteExW runas` does not work in this environment; use `elevate: false` for all apps
- SimHub auto-elevates via its own manifest and resists `taskkill` — the `SeDebugPrivilege` fallback in `Kill()` handles this

## Known limitations
- `Minimized` window style not implemented (requires `golang.org/x/sys/windows` for `StartupInfo`; currently treated as `Normal`)
- `stop` kills by image name — affects all instances of a process if multiple are running
- `processName` whitespace is not trimmed — accidental spaces will cause silent match failures
- Segment detection with `lataccel` method only uses lateral G — pure braking zones with no lateral load appear as straights (`latlon` default avoids this)
- S/F line wraparound: tiny corners (< 50 m) at the S/F line are auto-removed, but if the first and last segments are both straights they are not merged into one
- GPS quantisation in iRacing is systematic (same rounding each lap) so averaging more laps does not reduce noise in the `latlon` method — mitigated by bin-averaging, wide triplet spacing, and post-detection validation (steering/speed confirmation)
- Dynamic weather sessions do not populate `AirTemp` in the session YAML; PB weather shows track temp only in that case
- `pb.json` is never pruned — old car/track combos accumulate indefinitely

## Open improvements
- Exit codes: `RunStart`/`RunStop` currently always exit 0 even on partial failures
- CSV parsing in `IsRunning` and `parsePIDFromTasklist` is naive — works because PID is always field[1], but would break if Windows changes the column order
- Segment names are auto-labelled T1/S1/etc — no way to assign real corner names without hand-editing `trackmap.json`
- Same-direction corner complexes (e.g. Maggotts/Becketts) are not merged; only direction-reversing chicanes are detected
- `latlon` geo-method could be improved by using `VelocityX`/`VelocityY` channels (world-frame velocity) to compute heading-change rate instead of GPS curvature — avoids GPS quantisation entirely and should give a cleaner curvature proxy than bin-averaged lat/lon positions
- Sector times: group segments into logical sectors and show sector time per lap, so the coachable third of the track is immediately visible
- Exit speed vs straight entry speed: surface the direct relationship between corner exit speed and the subsequent straight's max speed — the primary measure of whether a corner exit is costing time on the following straight
- AI coaching via `-coach` flag: send the segment table, lap list, PB delta, and lap time trend to the Anthropic API and print actionable coaching feedback. Input is ~700 tokens (the existing analyze output as-is). Requires `ANTHROPIC_API_KEY` env var. Use `claude-haiku` for cost (~$0.001 per call).
