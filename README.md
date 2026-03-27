# SimAppLauncher
Application Launcher and Lap Analyser for Sim Racers

A lightweight Windows CLI tool that launches, monitors, and closes all your sim racing apps in one command, with built-in iRacing `.ibt` telemetry analysis. Designed for Stream Deck integration.

## Features

- **Sequential launch** with per-app configurable delays
- **Idempotent start** — skips apps already running, no duplicate instances
- **Status check** — shows running/stopped state and PID for each app
- **Elevated process support** — kills processes that auto-elevate (e.g. SimHub) via `SeDebugPrivilege`
- **Stream Deck ready** — simple CLI commands, no interactive UI
- **Lap analysis** — parse `.ibt` telemetry, extract zone-by-zone stats, compare laps, flag out/in laps

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

The `analyze` subcommand parses iRacing `.ibt` telemetry and prints a zone-by-zone breakdown. Out laps and in laps are automatically detected and excluded from best-lap selection.

### Usage

```powershell
# Best flying lap (auto-selected)
.\simapplauncher.exe analyze "session.ibt"

# Specific lap number
.\simapplauncher.exe analyze -lap 3 "session.ibt"

# Side-by-side comparison of two laps
.\simapplauncher.exe analyze -compare 2,3 "session.ibt"
```

### Example output

```
Driver:  Ricky Maw
Car:     Porsche 718 Cayman GT4
Track:   Sebring International Raceway
Samples: 9612 at 60 Hz

Laps:
  Lap  1: 1:55.123 (6908 samples) [flying lap]
  Lap  2: 1:54.887 (6891 samples) [flying lap]
  Lap  3: 0:04.550 (274 samples)  [in lap]

Selecting best lap: Lap 2 (1:54.887)

Lap 2 — 1:54.887

 Zone | Dist  | EntSpd | MinSpd | ExtSpd | Gear | Brake | Thr  | LatG | ABS | Coast
------|-------|--------|--------|--------|------|-------|------|------|-----|------
   1  |   5%  |  247.0 |   89.1 |  134.3 |    2 |   87% |  100% | 2.31 |   2 |     0
   2  |  10%  |  134.3 |  134.3 |  187.4 |    3 |    0% |  100% | 0.81 |   0 |     0
  ...
```

### Zone table columns

| Column | Description |
|---|---|
| Zone / Dist | Zone index and track position (5% increments) |
| EntSpd / MinSpd / ExtSpd | Speed at zone entry, apex, and exit (km/h) |
| Gear | Most common gear through the zone |
| Brake% | Peak brake pedal pressure |
| Thr% | Peak throttle |
| LatG | Peak lateral G-force |
| ABS | Number of samples where ABS was active |
| Coast | Samples with no throttle and no brake (neither > 5%) |

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
    main.go          # entry point, flag parsing, subcommand dispatch
    analyze.go       # RunAnalyze — analyze subcommand implementation
  ibtdump/
    main.go          # dev tool: dump .ibt variables and samples as CSV

internal/
  config/
    config.go        # Config/App structs and validation
    load.go          # JSON loader
    load_test.go
  launcher/
    launcher.go      # ProcessManager interface, RunStart/RunStop/RunStatus
    process_windows.go  # Spawn, IsRunning, Kill (+ SeDebugPrivilege fallback)
    elevate_windows.go  # UAC elevation via ShellExecuteExW
    output.go        # Formatted print helpers
    launcher_test.go
    output_test.go
    e2e_windows_test.go
  ibt/
    ibt.go           # .ibt binary parser (header, variable descriptors, samples)
    sample.go        # Per-sample typed accessors (Float32, Int, Bool, ...)
    vartype.go       # iRacing variable type constants and sizes
    ibt_test.go
  analysis/
    lap.go           # Lap extraction, out/in lap detection, session metadata
    zones.go         # Zone statistics and lap-delta computation
    analysis_test.go

launcher.config.json
simapplauncher.exe
```

---

## Known Limitations

- `Minimized` window style is not implemented — treated as `Normal` (requires `golang.org/x/sys/windows`)
- `stop` kills by image name — all instances are killed if multiple are running
- `processName` whitespace is not trimmed — accidental spaces cause silent failures
- `start`/`stop` always exit 0 even on partial failures
