# SimAppLauncher
Application Launcher and Lap Analyser for Sim Racers

A lightweight Windows CLI tool that launches, monitors, and closes all your sim racing apps in one command, with built-in iRacing `.ibt` telemetry analysis. Designed for Stream Deck integration.

## Features

- **Sequential launch** with per-app configurable delays
- **Idempotent start** — skips apps already running, no duplicate instances
- **Status check** — shows running/stopped state and PID for each app
- **Elevated process support** — kills processes that auto-elevate (e.g. SimHub) via `SeDebugPrivilege`
- **Stream Deck ready** — simple CLI commands, no interactive UI
- **Lap analysis** — parse `.ibt` telemetry, extract per-segment stats, compare laps, flag out/in laps
- **Geometry-based track segmentation** — auto-detects corners and straights from lateral G; cached in `trackmap.json` and refined across sessions

## Requirements

- Windows 10/11
- [Go 1.21+](https://go.dev/dl/) (to build from source)

## Build

```powershell
go build -o simapplauncher.exe ./cmd/simapplauncher
```

## Subcommands

```
simapplauncher [-config <path>] <start|stop|status|analyze>
```

| Subcommand | Description |
|---|---|
| `start` | Launch all configured apps in order |
| `stop` | Kill all configured apps |
| `status` | Print running/stopped state and PID for each app |
| `analyze` | Parse an `.ibt` file and print lap telemetry |

---

## App Launcher

### Usage

```powershell
.\simapplauncher.exe start
.\simapplauncher.exe stop
.\simapplauncher.exe status
.\simapplauncher.exe -config "D:\other\launcher.config.json" start
```

### Example output

```
> simapplauncher start
  [+] SimHubWPF            ... launched (pid 41512)
  [+] Trading Paints       ... launched (pid 43996)
  [=] iRacingUI            ... already running (pid 43876)
  [+] MarvinsAIRA          ... launched (pid 46676)

Done. 4/4 apps running.

> simapplauncher status
  SimHubWPF            RUNNING  41512
  Trading Paints       RUNNING  43996
  iRacingUI            RUNNING  43876
  MarvinsAIRA          RUNNING  46676

> simapplauncher stop
  [-] SimHubWPF            ... closed
  [-] Trading Paints       ... closed
```

### Stream Deck integration

Use the **Open** action pointing directly at `simapplauncher.exe` — no PowerShell wrapper needed.

| Setting | Value |
|---|---|
| App/File | `G:\RACING\SimAppLauncher\simapplauncher.exe` |
| Arguments | `start` (or `stop`) |

The binary resolves `launcher.config.json` relative to its own location via `os.Executable()`, so the working directory doesn't matter.

---

## Lap Analysis

The `analyze` subcommand parses iRacing `.ibt` telemetry and prints a segment-by-segment breakdown. Corners and straights are detected automatically from lateral acceleration and cached in `trackmap.json` — no manual configuration needed.

### Usage

```powershell
# Best flying lap (auto-selected)
.\simapplauncher.exe analyze "session.ibt"

# Specific lap number
.\simapplauncher.exe analyze -lap 3 "session.ibt"

# Side-by-side comparison of two laps
.\simapplauncher.exe analyze -compare 2,3 "session.ibt"

# Force re-detection of track segments from this session
.\simapplauncher.exe analyze -update-map "session.ibt"
```

### Example output

```
Driver:  Ricky Maw
Car:     Porsche 718 Cayman GT4
Track:   Sebring International Raceway
Samples: 137837 at 60 Hz
Map:     17 segs — geometry: high (47 laps, 6 sessions) — match: 94%

Laps:
  Lap  1: 2:34.200 (9253 samples) [out lap]
  Lap  2: 3:35.917 (12955 samples) [flying lap]
  Lap  3: 2:25.033 (8704 samples) [flying lap]
  ...
  Lap 17: 1:01.317 (3680 samples) [in lap]

Selecting best lap: Lap 5 (2:11.367)

Lap 5 — 2:11.367

 Seg  | Name         |  Entry →   Exit | EntSpd | MinSpd | ExtSpd | Gear | Brake |  Thr  | LatG | ABS | Coast
------|--------------|-----------------|--------|--------|--------|------|-------|-------|------|-----|------
   1  | S1           |   0.0% →   6.0%  |  202.7 |  202.7 |  203.6 |    5 |    0% |  100% | 0.31 |   0 |     0
   2  | T1           |   6.0% →  14.2%  |  241.3 |   89.1 |  134.3 |    2 |   87% |  100% | 2.31 |  59 |    25
   3  | T2           |  14.2% →  21.9%  |  134.3 |   86.3 |  112.1 |    3 |  100% |  100% | 1.78 |  77 |    81
   4  | T3           |  21.9% →  28.7%  |  153.9 |  153.9 |  215.1 |    4 |    0% |  100% | 1.04 |   0 |     0
   5  | S2           |  28.7% →  33.4%  |  215.1 |  103.2 |  103.2 |    5 |  100% |  100% | 0.36 |  48 |     0
   6  | T4           |  33.4% →  36.0%  |  102.4 |   64.3 |  129.0 |    2 |   79% |  100% | 1.76 |  79 |     9
   7  | T5-6         |  36.0% →  43.4%  |  129.3 |  129.3 |  209.2 |    4 |    0% |  100% | 1.98 |   0 |     0
  ...
```

### Segment table columns

| Column | Description |
|---|---|
| Seg | Sequential segment index |
| Name | Auto-detected label: S1/S2… (straights), T1/T2… (corners), T3-T4 (chicane) |
| Entry → Exit | Track position range as % of lap distance |
| EntSpd / MinSpd / ExtSpd | Speed at segment entry, minimum, and exit (km/h) |
| Gear | Most common gear through the segment |
| Brake% | Peak brake pedal pressure |
| Thr% | Peak throttle |
| LatG | Peak lateral G-force |
| ABS | Samples where ABS was active (÷ 60 = seconds) |
| Coast | Samples with neither throttle nor brake > 5% |

### Track map

On first run for a new track, `analyze` auto-detects corners and straights from the lateral G profile across all flying laps in the session. The result is saved to `trackmap.json` next to the binary and reused for all future sessions on that track.

The `Map:` header line shows two confidence signals:

```
Map:     17 segs — geometry: high (47 laps, 6 sessions) — match: 94%
```

| Signal | Description |
|---|---|
| **geometry** | How well-established the stored map is — `low` (< 3 laps), `moderate` (3–10), `high` (> 10) |
| **match %** | How closely this session's lateral G profile matches the stored boundaries. < 70% triggers a warning. |

Each track's data accumulates automatically across sessions. Running `analyze` on the same `.ibt` file multiple times does not inflate the counters — sessions are deduplicated by their start timestamp.

If boundaries drift or you want to regenerate them from a new session, run with `-update-map`.

### Segment names

Segments are labelled automatically (T1, S1, T3-T4, etc.). To use real corner names, hand-edit the `name` field in `trackmap.json` — the file is plain JSON and your edits are preserved across future sessions.

### Out/in lap detection

A lap is flagged as an **out lap** if entry speed < 5 m/s (pit/grid exit), an **in lap** if exit speed < 5 m/s (pit entry). Either type is shown in the lap list but skipped by best-lap auto-selection. You can still force-analyze them with `-lap N`.

### Driver/car resolution in multi-class sessions

Set `"driver"` in the config to your iRacing username. The `analyze` command matches it case-insensitively against `UserName` entries in the session YAML and picks the correct car. Without it, it falls back to `DriverCarIdx`.

```json
{ "driver": "Ricky Maw", "apps": [...] }
```

---

## Configuration

`launcher.config.json` lives next to the binary. The `-config` flag overrides this.

```json
{
  "driver": "Ricky Maw",
  "apps": [
    {
      "name": "SimHubWPF",
      "path": "G:\\Program Files (x86)\\SimHub\\SimHubWPF.exe",
      "args": "",
      "windowStyle": "Normal",
      "delayMs": 1500,
      "elevate": false,
      "processName": "SimHubWPF"
    },
    {
      "name": "iRacingUI",
      "path": "N:\\Program Files (x86)\\iRacing\\ui\\iRacingUI.exe",
      "args": "",
      "windowStyle": "Normal",
      "delayMs": 0,
      "elevate": false,
      "processName": "iRacingUI"
    }
  ]
}
```

### Fields

| Field | Description |
|---|---|
| `driver` | Your iRacing `UserName`. Used by `analyze` to find your car in multi-class sessions. Optional. |
| `apps[].name` | Display name shown in CLI output (required) |
| `apps[].path` | Full path to the executable (required) |
| `apps[].args` | Command-line arguments as a space-separated string |
| `apps[].windowStyle` | `Normal` or `Hidden` (default: `Normal`) |
| `apps[].delayMs` | Milliseconds to wait after launch before continuing (≥ 0) |
| `apps[].elevate` | Launch via `ShellExecuteExW runas` |
| `apps[].processName` | Exe stem for `tasklist`/`taskkill`. Falls back to `name` if empty. Set this if the app spawns a child with a different image name. |

---

## Testing

```powershell
# Unit tests — no real processes or files required
go test ./...

# Full stack e2e — launches and closes your actual apps (~20s)
go test -tags e2e -v ./internal/launcher/ -run TestE2E_FullStack -timeout 120s
```

---

## Project Structure

```
cmd/
  simapplauncher/
    main.go              # entry point, flag parsing, subcommand dispatch
    analyze.go           # RunAnalyze — analyze subcommand implementation
    analyze_test.go
  ibtdump/
    main.go              # dev tool: dump .ibt variables and samples as CSV

internal/
  config/
    config.go            # Config/App structs and validation
    load.go              # JSON loader
    load_test.go
  launcher/
    launcher.go          # ProcessManager interface, RunStart/RunStop/RunStatus
    process_windows.go   # Spawn, IsRunning, Kill (+ SeDebugPrivilege fallback)
    elevate_windows.go   # UAC elevation via ShellExecuteExW
    output.go            # Formatted print helpers
    launcher_test.go
    output_test.go
    e2e_windows_test.go
  ibt/
    ibt.go               # .ibt binary parser (header, variable descriptors, samples)
    sample.go            # Per-sample typed accessors (Float32, Int, Bool, ...)
    vartype.go           # iRacing variable type constants and sizes
    ibt_test.go
  analysis/
    lap.go               # Lap extraction, out/in lap detection, session metadata, ParseTrackLength
    zones.go             # SegmentStats, SegmentDeltas (+ legacy ZoneStats/ZoneDeltas)
    analysis_test.go
  trackmap/
    trackmap.go          # Segment/TrackMap types, trackmap.json load/save, confidence
    detect.go            # DetectFromMultiple, Detect, MatchScore
    trackmap_test.go

launcher.config.json
trackmap.json            # auto-created on first analyze run
simapplauncher.exe
```

---

## Known Limitations

- `Minimized` window style is not implemented — treated as `Normal` (requires `golang.org/x/sys/windows`)
- `stop` kills by image name — all instances are killed if multiple are running
- `processName` whitespace is not trimmed — accidental spaces cause silent failures
- `start`/`stop` always exit 0 even on partial failures
- Segment detection uses only lateral G — pure braking zones with no lateral load may appear as straights
- Same-direction corner complexes (e.g. Maggotts/Becketts) are not auto-merged; only direction-reversing chicanes are detected
- Segment names are auto-labelled T1/S1 etc — use real corner names by hand-editing `trackmap.json`
