# MotorHome
Application Launcher and Lap Analyser for Sim Racers

A Windows CLI tool that launches sim racing apps in sequence, analyses iRacing `.ibt` telemetry, and captures voice notes on track. Designed for Stream Deck integration.

## Features

- **Sequential launch** with per-app configurable delays
- **Idempotent start** — skips apps already running
- **Status check** — running/stopped state and PID per app
- **Elevated process support** — kills auto-elevating processes (e.g. SimHub) via `SeDebugPrivilege`
- **Lap analysis** — phase-based segment stats, PB tracking
- **GPS-based track segmentation** — auto-detects corners and straights from GPS curvature; cached in `trackmap.json`
- **Voice notes** — hold a hotkey to record, auto-transcribed via Whisper, stamped with track position and segment

## Requirements

- Windows 10/11
- [Go 1.21+](https://go.dev/dl/) (to build from source)
- [whisper-cli](https://github.com/ggerganov/whisper.cpp) (for voice notes)

## Build

```powershell
go build -o motorhome.exe ./cmd/motorhome
```

## Subcommands

```
motorhome [-config <path>] <start|stop|status|analyze|notes>
```

| Subcommand | Description |
|---|---|
| `start` | Launch all configured apps in order |
| `stop` | Kill all configured apps |
| `status` | Print running/stopped state and PID |
| `analyze` | Parse an `.ibt` file and print lap telemetry |
| `notes` | Record voice notes stamped with track position |

---

## App Launcher

```powershell
.\motorhome.exe start
.\motorhome.exe stop
.\motorhome.exe status
.\motorhome.exe -config "D:\other\launcher.config.json" start
```

### Example output

```
> motorhome start
  [+] SimHubWPF            ... launched (pid 41512)
  [+] Trading Paints       ... launched (pid 43996)
  [=] iRacingUI            ... already running (pid 43876)

Done. 3/3 apps running.

> motorhome status
  SimHubWPF            RUNNING  41512
  Trading Paints       RUNNING  43996
  iRacingUI            RUNNING  43876
```

### Stream Deck integration

Use the **Open** action pointing directly at `motorhome.exe` — no PowerShell wrapper needed.

| Setting | Value |
|---|---|
| App/File | `G:\RACING\SimAppLauncher\motorhome.exe` |
| Arguments | `start` (or `stop`) |

---

## Lap Analysis

```powershell
.\motorhome.exe analyze                          # most recent .ibt
.\motorhome.exe analyze 2                        # 2nd most recent
.\motorhome.exe analyze session.ibt              # specific file
.\motorhome.exe analyze -lap 3 session.ibt       # specific lap
.\motorhome.exe analyze -update-map session.ibt  # force re-detect segments
```

### Example output

```
Driver:  Ricky Maw
Car:     Porsche 718 Cayman GT4
Track:   Donington Park
Map:     12 segs — geometry: high (47 laps, 6 sessions) — match: 94%
PB:      1:06.843 (2026-03-31) — this session: 1:07.102 (+0.259)

Laps:
  Lap  1: 1:12.400 [out lap]
  Lap  2: 1:07.500 [flying lap]
  Lap  3: 1:07.102 [flying lap]

Selecting best lap: Lap 2 (1:07.500)

 Seg | Name  | Phase |   Spd |  Brk | PkBrk |  Thr | TC%  | LatG | Steer° | Corr | Lock | Spin | Coast
-----|-------|-------|-------|------|-------|------|------|------|--------|------|------|------|------
   1 | S1    | full  | 187.4 |   0% |    0% | 100% |   0% | 0.28 |    2.1 |    0 |    0 |    0 |  0.0
   2 | T1    | entry | 196.4 |  94% |   98% |   0% |   0% | 1.42 |   38.7 |    2 |   12 |    0 |  0.1
   2 | T1    | mid   | 82.1  |   0% |    0% |  22% |   3% | 2.14 |   62.4 |    0 |    0 |    0 |  0.4
   2 | T1    | exit  | 112.0 |   0% |    0% |  87% |   5% | 1.68 |   31.5 |    1 |    0 |    3 |  0.0
```

### Segment table columns

| Column | Description |
|---|---|
| Phase | Corner phase: `entry`, `mid`, `exit` for corners/chicanes; `full` for straights |
| Spd | Speed in km/h — entry speed for entry phase, minimum (apex) for mid, exit speed for exit, average for straights |
| Brk | Fraction of samples with brake > 2% |
| PkBrk | Peak brake pressure (0--100%) |
| Thr | Fraction at full throttle (> 95%) |
| TC% | Fraction of samples where traction control is cutting throttle (ThrottleRaw - Throttle > 2%) |
| LatG | Mean abs lateral G over the phase |
| Steer° | Mean absolute steering angle in degrees |
| Corr | Steering correction count -- rapid direction changes indicating car instability |
| Lock | Samples where any wheel speed < 95% of vehicle speed under braking (lockup) |
| Spin | Samples where any wheel speed > 105% of vehicle speed under power (wheelspin) |
| Coast | Seconds with neither throttle nor brake > 5% |

### AI Coaching

Claude can run the analysis and deliver structured coaching feedback automatically.

**Prerequisite:** [Claude Code](https://claude.com/claude-code) running in this repo directory.

**What to say:**
> "Coach me on my latest session"
> "Analyse my last session and give me coaching feedback"

Claude runs `analyze`, identifies the best and most-recent flying laps, reads `coach.md`, and delivers per-segment findings and a **Top 3 Actions** list.

---

## Voice Notes

```powershell
.\motorhome.exe notes set-hotkey   # press a key to bind it; saves to config
.\motorhome.exe notes              # start listening; hold hotkey to record
```

Hold the configured hotkey while driving to record a voice note. On release, the note is transcribed by Whisper and saved to a JSON file in `notes/`.

```
[note] too much mid-corner understeer here
```

Notes are stored per-session alongside the `.ibt` file name they relate to.

---

## Configuration

`launcher.config.json` lives next to the binary. Override with `-config <path>`.

```json
{
  "driver": "Ricky Maw",
  "ibtDir": "C:\\Users\\ricky\\Documents\\iRacing\\telemetry",
  "hotkey": "F13",
  "whisperPath": "G:\\RACING\\whisper\\whisper-cli.exe",
  "whisperModel": "G:\\RACING\\whisper\\ggml-base.en.bin",
  "apps": [
    {
      "name": "SimHubWPF",
      "path": "G:\\Program Files (x86)\\SimHub\\SimHubWPF.exe",
      "args": "",
      "windowStyle": "Normal",
      "delayMs": 1500,
      "elevate": false,
      "processName": "SimHubWPF"
    }
  ]
}
```

| Field | Description |
|---|---|
| `driver` | iRacing `UserName` — used by `analyze` to find your car in multi-class sessions |
| `ibtDir` | Directory scanned for `.ibt` files when no path is passed to `analyze` |
| `hotkey` | Key name for voice notes (e.g. `"F13"`, `"ScrollLock"`) — set via `notes set-hotkey` |
| `whisperPath` | Path to `whisper-cli.exe` |
| `whisperModel` | Path to Whisper `.bin` model file |
| `apps[].processName` | Exe stem for `tasklist`/`taskkill`. Falls back to `name`. Set this if the app spawns a child process with a different image name. |

---

## Testing

```powershell
go test ./...                                                                          # unit tests
go test -tags e2e -v ./internal/launcher/ -run TestE2E_FullStack -timeout 120s        # full stack e2e
```

---

## Package Overview

| Package | Description | Details |
|---|---|---|
| `cmd/motorhome` | Entry point, flag parsing, subcommand dispatch | [README](cmd/motorhome/README.md) |
| `internal/config` | Config loading and validation | [README](internal/config/README.md) |
| `internal/launcher` | Process spawn/kill/status via `tasklist`/`taskkill` | [README](internal/launcher/README.md) |
| `internal/ibt` | Low-level `.ibt` binary parser | [README](internal/ibt/README.md) |
| `internal/analysis` | Lap extraction, phase-based segment stats, brake entry detection | [README](internal/analysis/README.md) |
| `internal/trackmap` | GPS-based corner detection; `trackmap.json` store | [README](internal/trackmap/README.md) |
| `internal/pb` | Personal best tracking; `pb.json` store | [README](internal/pb/README.md) |
| `internal/notes` | Voice note types and JSON persistence | [README](internal/notes/README.md) |
| `internal/iracing` | Live telemetry via iRacing shared memory | [README](internal/iracing/README.md) |
| `internal/audio` | Microphone recording via WinMM | [README](internal/audio/README.md) |

---

## Known Limitations

- `Minimized` window style not implemented — treated as `Normal`
- `stop` kills by image name — all instances killed if multiple are running
- `processName` whitespace not trimmed — accidental spaces cause silent failures
- `start`/`stop` always exit 0 even on partial failures
- Same-direction corner complexes (e.g. Maggotts/Becketts) not auto-merged; only direction-reversing chicanes are detected
- Segment names are auto-labelled T1/S1 etc — hand-edit `trackmap.json` for real corner names
