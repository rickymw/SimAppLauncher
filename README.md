# MotorHome
Application Launcher and Lap Analyser for Sim Racers

A Windows CLI tool that launches sim racing apps in sequence, analyses iRacing `.ibt` telemetry, and captures voice notes on track. Designed for Stream Deck integration.

## Features

- **Sequential launch** with per-app configurable delays
- **Idempotent start** — skips apps already running
- **Status check** — running/stopped state and PID per app
- **Elevated process support** — kills auto-elevating processes (e.g. SimHub) via `SeDebugPrivilege`
- **Lap analysis** — phase-based segment stats, PB tracking with per-segment delta comparison
- **Consistency analysis** — lap-to-lap spread per corner phase, ranking the corners you repeat least well
- **Fuel and stint planning** — per-lap burn, remaining laps at average *and* worst-lap rates, and whether the tank covers a given race distance
- **AI coaching in one command** — `coach` bundles the framework and the data into a single brief; no API key, no network call
- **GPS-based track segmentation** — auto-detects corners and straights from GPS curvature; cached in `trackmap.json`
- **Voice notes** — press a hotkey to record, auto-transcribed via Whisper, and placed on the exact lap and corner you were driving when you spoke
- **PB store management** — list, inspect, prune, and diff the setup you're running now against the setup that set your PB
- **JSON output** — the whole analysis as a structured document for AI coaching or any other downstream tool
- **Web interface** — `motorhome gui` serves a local dashboard covering the rig controls, the settings file, session analysis, live gaps and the PB store. No dependencies, no build step, loopback only

## Requirements

- Windows 10/11
- [Go 1.21+](https://go.dev/dl/) (to build from source)
- [whisper-cli](https://github.com/ggml-org/whisper.cpp) (for voice notes)

## Build

```powershell
go build -o motorhome.exe ./cmd/motorhome
```

## Subcommands

```
motorhome [-config <path>] <start|stop|status|analyze|coach|pb|notes|live|camera|usb|gui>
```

| Subcommand | Description |
|---|---|
| `start` | Launch all configured apps in order |
| `stop` | Kill all configured apps |
| `status` | Print running/stopped state and PID |
| `analyze` | Parse an `.ibt` file and print lap telemetry |
| `coach` | Emit a self-contained coaching brief for an AI assistant to act on |
| `pb` | List, inspect, diff and prune the personal-best store |
| `notes` | Record voice notes stamped with track position |
| `live` | Live position + gap in seconds to the car directly ahead and behind on track |
| `camera` | Restart a stuck/frozen webcam by restarting the Windows Camera Frame Server |
| `usb` | List the sim-racing USB devices and enable/disable them individually |
| `gui` | Serve the web interface on `127.0.0.1` — rig control, settings, analysis, live gaps, personal bests |

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
.\motorhome.exe analyze -lap pb                  # render stored PB from pb.json
.\motorhome.exe analyze -update-map session.ibt  # force re-detect segments
.\motorhome.exe analyze -dump T3 session.ibt     # dump T3 telemetry to CSV (written next to the .ibt)
.\motorhome.exe analyze -dump T3 -dump-all       # dump T3 from every comparable lap into one CSV
.\motorhome.exe analyze -trace T3,T4             # print T3/T4 sample telemetry inline (60Hz)
.\motorhome.exe analyze -trace T3 -hz 20         # same, downsampled to 20Hz
.\motorhome.exe analyze -json                    # structured output instead of tables
.\motorhome.exe analyze -note-lag 3              # shift voice notes 3s earlier when placing them
```

The full output is automatically copied to the system clipboard (via `clip.exe`) — paste straight into Claude or another window without redirecting to a file. `(copied to clipboard)` is printed to stderr on success.

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

Sectors:

  Lap           S1        S2        S3         Lap
  ------ --------- --------- ---------  ----------
  2        22.104    23.291    22.105     1:07.500
  3        22.011*   23.402    21.689*    1:07.102
  best      22.011    23.291    21.689    1:06.991

  Theoretical best 1:06.991 from sectors set on laps 3 2 3

Selecting best lap: Lap 2 (1:07.500)

 Name | Phase | Spd         | OnBrk | PkBrk | Thr% | LatG | Wheel° | Corr | ABS  | Lock | Spin | Coast
------|-------|-------------|-------|-------|------|------|--------|------|------|------|------|------
 S1   | full  |   187→  190 |    0% |    0% | 100% | 0.28 |    2.1 |    0 |    0 |    0 |    0 | 0.00s
 T1   | entry |   196→  126 |   94% |   98% |   0% | 1.42 |   38.7 |    2 |    0 |   12 |    0 | 0.10s
 T1   | mid   |    82→   91 |    0% |    0% |  22% | 2.14 |   62.4 |    0 |    0 |    0 |    0 | 0.40s
 T1   | exit  |   112→  140 |    0% |    0% |  87% | 1.68 |   31.5 |    1 |    0 |    0 |    3 | 0.00s

vs PB:

 Name | Phase | dSpd        | dBrk  | dPkBr | dThr | dLatG  | dCorr | dABS | dLck | dSpn | dCoast
------|-------|-------------|-------|-------|------|--------|-------|------|------|------|-------
 S1   | full  |    +3→   +2 |    +0 |    +0 |   +0 | +0.01 |    +0 |   +0 |   +0 |   +0 | +0.00s
 T1   | entry |    -4→   +2 |    +2 |    -1 |   +0 | -0.05 |    +1 |   +0 |   +3 |   +0 | +0.05s
 T1   | mid   |    +1→   -1 |    +0 |    +0 |   -3 | -0.08 |    +0 |   +0 |   +0 |   +0 | +0.10s
 T1   | exit  |    -2→   -3 |    +0 |    +0 |   -5 | +0.02 |    +0 |   +0 |   +0 |   +1 | +0.00s

Corner Exit -> Straight Peak:
  Corner  ExitSpd  Straight  PeakSpd
  T1       112.0   S2         198.4
```

### Corner names

Corners are auto-labelled `T1`, `T2`, … in track order from the start/finish line. These are **positional labels, not iRacing's official turn numbers** — detection merges some complexes, so the counts can differ. The output shows both:

```
Turns:   11 corners detected; iRacing reports 14 turns — labels are positional, not official
```

To give corners their real numbers and names, add a `cornerNames` list to the track's entry in `trackref.json` — one entry per detected corner, in track order:

```json
"Road America": {
  "corners": 11,
  "cornerNames": ["T1", "", "T5", "", "", "T8 Carousel", "", "T11 Kink", "", "", ""]
}
```

An empty entry keeps the generated label, so you can name corners as you learn them. The names then appear everywhere — phase table, vs-PB table, corner-exit table, `-dump` filenames, and the name you pass to `coach -segment`. If the list length doesn't match the detected corner count it is refused with a warning rather than applied out of step, which would mislabel every corner after the mismatch.

### Sector table

Per-sector times for every flying lap, using **iRacing's own sector boundaries** (read from the session YAML's `SplitTimeInfo` block), so they agree with the sim's timing. The fastest time in each sector is marked `*`, and the `best` row sums to the theoretical best lap — the time available if you strung your best sectors together. Shown even when no track map exists.

### Segment table columns

| Column | Description |
|---|---|
| Phase | Corner phase: `entry`, `mid`, `exit` for corners/chicanes; `full` for straights |
| Spd | Speed in km/h — entry speed for entry phase, minimum (apex) for mid, exit speed for exit, average for straights |
| OnBrk | % of phase time with brake applied (> 2%) |
| PkBrk | Peak brake pressure (0--100%) |
| Thr | Fraction at full throttle (> 95%) |
| LatG | Mean abs lateral G over the phase |
| Wheel° | Peak steering wheel angle in degrees (divide by steering ratio for road wheel angle) |
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

Claude runs `coach`, which emits one self-contained brief — session orientation, the `coach.md` framework, and the analysis as JSON — and delivers per-segment findings and a **Top 3 Actions** list. It's a single command; there's no separate `analyze` run or `coach.md` read.

Once that names the corner costing the most, ask it to go deeper on that one — `coach -segment T3` narrows the brief and inlines the corner's sample-level telemetry, so the answer moves from "your T3 exit varies" to which lap did what, and when.

---

### Consistency

The phase table describes your best lap. The consistency table describes how repeatably you drive it — usually where the larger, more durable gain is.

```
Consistency (2 laps: 3, 4):

 Name | Phase | N  | EntSpd      | ExitSpd     | PkBrk | LatG  | Coast | Best exit
------|-------|----|-------------|-------------|-------|-------|-------|-----------
 T2   | entry |  2 | 192.3 ± 9.7 |  85.1 ±50.9 | ± 2.2 | ±0.14 | ±0.14 | 121.0 (L4)
 T2   | mid   |  2 |  85.1 ±50.8 |  91.1 ±53.5 | ± 4.4 | ±0.52 | ±1.15 | 128.9 (L4)
 T3   | entry |  2 | 226.7 ± 1.9 |  53.3 ±14.0 | ± 0.0 | ±0.12 | ±0.00 |  63.2 (L4)

  Most variable exit speed: T2 mid (±53.5 km/h), T2 entry (±50.9 km/h)
```

Speeds are mean ± standard deviation; brake, lateral G and coast show spread only, since their averages are already in the phase table. The header names the laps used — a wider filter than the one feeding corner detection, so that a session where you were still improving still produces a spread.

### Fuel

```
Fuel:
  Used:      6.72 L total; 2.18 avg, 2.25 median, 2.25 worst per lap (2 measured laps)
  Remaining: 42.28 L → 19.4 laps at average, 18.8 at worst-lap rate
  Burn rate: 56.3 kg/h average
  For 12 laps: 29.31 L at the worst-lap rate (+1 lap margin) — 12.97 L in hand
```

Two figures for remaining laps, because planning a stint on the average runs dry half the time — the worst-lap rate is what a stint has to survive. `-fuel-laps N` adds the last line.

Consumption comes from the tank level at each lap's boundaries, not from the instantaneous burn-rate channel. If a lap refuels, its consumption is excluded and "remaining" is measured at the end of the last lap that didn't — otherwise a session that ends with a pit stop reports a full tank and zero fuel used.

### JSON output

`-json` emits the whole analysis — session metadata, laps, sectors, phases, vs-PB deltas, exit impact, tyres, consistency and notes — as a versioned document, so tools don't have to parse fixed-width tables.

```powershell
.\motorhome.exe analyze -json | ConvertFrom-Json
```

---

## AI Coaching

```powershell
.\motorhome.exe coach              # most recent session
.\motorhome.exe coach -lap 3       # a specific lap
.\motorhome.exe coach -no-framework  # data only
.\motorhome.exe coach -table       # turn-by-turn table, for a human
.\motorhome.exe coach -segment T3  # one corner, with its sample-level telemetry
.\motorhome.exe coach -segment T3,T4 -hz 20
```

Emits a single self-contained brief — session orientation, the full `coach.md` framework, and the analysis as JSON — for an AI assistant (Claude Code) to act on. **No API key and no network call:** the assistant reading the brief is the coach.

```
# Coaching brief — Porsche 718 Cayman GT4 at Phillip Island Circuit

You are the race engineer. Read the framework, then the session data below,
and deliver per-segment findings followed by a **Top 3 Actions** list.

## Session

- Laps: 5 total, 3 flying, 2 comparable
- Analysed: lap 4 (1:41.554, selected as best)
- PB: 1:41.554 set 2026-07-29 — this session is 0.000s off it
- Gaps: no sector times published for this session type
```

The `Gaps:` line names what's absent, so the coach doesn't build confident findings on data that isn't there. Edit `coach.md` to change how the analysis is interpreted — the brief embeds it, so there's one source of truth.

### `-table` — the same session, for a human

```
 Turn      | Speed in>min>out | Coast | Lock  | Spin  | ExitSD | Flags             | Grade
-----------|------------------|-------|-------|-------|--------|-------------------|------
 T1 Doohan | 201>199>201      | 0.20s |     - |     - |    6.6 |                   | A
 T2-3      | 184>106>169      | 1.10s | 3.07s |     - |   19.6 | coast lock spread | D
 T4 Honda  | 122>73>92        | 1.38s | 1.97s | 1.10s |    5.5 | coast lock spin   | D
 T8-9      | 205>115>115      | 0.58s | 4.32s |     - |   33.5 | lock spread       | C
```

One row per corner, then a sector-loss table and a ranking of the least repeatable exits. **The Grade is a triage hint, not a measurement** — it counts how many of five fixed thresholds a corner trips, and those thresholds are printed in the legend so a letter can be argued with. There is no "Fix" column on purpose: what to do about a flagged corner is coaching judgement, and generating it from thresholds would be a canned string dressed up as advice.

### `-segment` — zooming into one corner

The default brief tells you *which* corner is costing time. It can't tell you *what's happening in it* — a mean and a standard deviation describe a corner, they don't replay it. `-segment` drops the per-segment rows for everything else and inlines the corner's actual telemetry, every comparable lap overlaid:

```
## T2-3 (corner) — laps 5, 6, 7, 8, 9 at 60Hz

Lap,Dist%,Time,Speed,Throttle,Brake,Steer,Gear,LatG,LongG,ABS,Coast
5,0.1523,0.000,186.4,100,0,-2.1,4,-0.18,0.31,0,0
5,0.1532,0.017,186.9,100,0,-2.3,4,-0.15,0.30,0,0
```

That's the difference between "your T2-3 exit varies by 19 km/h" and "on lap 6 you were still at 12% throttle 0.4s after the apex you took at 60% on lap 5".

The brief says in its orientation that it was narrowed, so the assistant doesn't characterise the whole lap from one corner of it. `-hz N` trades resolution for size (60Hz default; a long complex across five laps runs to a few thousand rows). ABS and lock-ups survive downsampling — they're reported if they occurred anywhere in the window a row covers, not sampled at one instant.

`analyze -trace T3,T4` prints the same traces without the brief around them.

---

## Voice Notes

```powershell
.\motorhome.exe notes set-hotkey   # press a key to bind it; saves to config
.\motorhome.exe notes              # start listening; press hotkey to start/stop
```

Press the configured hotkey while driving to record a voice note; press again to stop. The note is transcribed by Whisper and saved to a JSON file in `notes/`, named after the `.ibt` it belongs to.

```
[note] too much mid-corner understeer here
```

### Notes on track

`analyze` then places each note on the lap and corner you were driving when you spoke it:

```
Notes (3 from porsche718gt4_phillipisland 2026-07-29 19-48-50.json):

  Lap | Where | Lap% | Note
  ----|-------|------|-----
    2 | T8    |  79% | lost the rear on exit here
    3 | S5    |  53% | braking too early
    - | -     |    - | good session overall

  1 note falls outside the telemetry recording — no track position.
```

Placement uses the moment you *started* speaking, minus a small reaction-time allowance (`-note-lag`, default 2s) — a note about a corner is always recorded after that corner, so an uncorrected timestamp lands late. Notes spoken before the recording started or after it stopped keep their text and are reported as unplaced rather than dropped.

---

## Personal Bests

```powershell
.\motorhome.exe pb                       # list every stored PB
.\motorhome.exe pb show watkins          # full record: setup + phase table
.\motorhome.exe pb diff                  # what have I changed since the PB?
.\motorhome.exe pb prune -older-than 180 # preview; add -apply to actually remove
```

`pb.json` grows one entry per car/track and is never trimmed by `analyze`. These commands are the way to read it back.

`pb diff` answers the question that matters mid-session — *what have I changed since the lap I'm trying to beat?*

```
  Setting                               PB         Now
  ------------------------------------  ---------  ---
  Chassis/Rear/ArbBlades                5          6
  Chassis/Rear/WingAngle                4 deg      2 deg
  Chassis/LeftRear/BumpStiffness        +7 clicks  +6 clicks

  16 settings differ from the PB setup.
  13 session-state readings hidden (hot pressures, tyre temps, tread) — pass -all to see them.
```

iRacing writes end-of-session tyre readings back into the setup block, so those are filtered out by default — otherwise a diff of two *identical* setups still reports every corner's pressure and temperature.

`prune` previews by default and only writes with `-apply`: removing an entry also discards its accumulated brake points and the setup that produced the lap.

---

## Live Gap

```powershell
.\motorhome.exe live              # one-shot: position + gap to car ahead/behind
.\motorhome.exe live -watch       # stream at 5 Hz until Ctrl-C
.\motorhome.exe live -watch -hz 10
.\motorhome.exe live -raw         # dump raw LiveData fields (diagnostic)
```

Reads iRacing's shared-memory segment and prints your current position, lap, and the gap in seconds to the car directly ahead and behind you on track. Names are drawn from the session info YAML.

```
Track : Silverstone Grand Prix | Car: Porsche 718 Cayman GT4
Pos 7/24 (class 3/12)  Lap 4 @  34.2%
Ahead : #22   John Doe             +1.342s
Behind: #07   Jane Smith           -0.891s
```

Gap math uses each car's `CarIdxEstTime` when both are on the same lap, otherwise falls back to distance × total-lap estimate. In a solo practice (no other cars) you'll see `(none)` for ahead/behind — that's expected.

---

## Camera Restart

```powershell
.\motorhome.exe camera
```

Restarts the Windows `FrameServer`/`FrameServerMonitor` services — the shared pipeline every app uses to access a webcam — to clear a stuck or frozen camera without unplugging it.

The case this exists for: when the webcam is redirected into a **Remote Desktop** session, leaving the meeting does not release it — `mstsc.exe` keeps holding the camera indefinitely, so every local app finds it busy and nothing frees it short of a reboot. This command tears down that stale handle.

**Redirection has two ends.** The camera is attached to the RDP client, but the remote machine runs its own frame server for the redirected device. If apps *inside* the session say "in use or unavailable", the stall is on the remote machine and running this locally will not help — copy `motorhome.exe` over and run it there. It needs no config file or any other repo file, just the exe.

Reconnecting the RDP session does **not** clear that one: the frame server is machine-wide, not per-session, so the stale handle survives the session being rebuilt. Running `camera` on the remote machine does clear it, without a reboot. This does **not** require an elevated process, only a one-time grant of `SERVICE_START`/`SERVICE_STOP` rights on those two services to your account (a full PnP device disable/enable needs real admin rights — obtainable here by re-execing elevated, as `motorhome usb` does, but a bigger hammer than this needs):

```powershell
# One-time setup — run once, elevated. Look up your SID first:
([System.Security.Principal.WindowsIdentity]::GetCurrent()).User.Value

# Then, substituting that SID, grant start/stop rights on both services:
sc.exe sdset FrameServer "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)(A;;RPWPLC;;;<YOUR-SID>)"
sc.exe sdset FrameServerMonitor "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)(A;;RPWPLC;;;<YOUR-SID>)"
```

```
  [+] FrameServer          ... restarted
  [+] FrameServerMonitor   ... restarted

Done. 2/2 services restarted.
```

Takes ~0.5s when the camera is idle. If an app is holding the camera the stop can take up to ~30s — Windows waits for the holder to release the device before the service will stop — and a progress line explains the wait rather than leaving the window looking hung.

If the services are already stopped the command is a no-op — Windows demand-starts them on next camera access, so there is no stuck state to clear:

```
  [=] FrameServer          ... already stopped
  [=] FrameServerMonitor   ... already stopped

Camera pipeline was not running — nothing to restart.
Windows starts it on demand, so the camera will initialise fresh on next use.
```

See [internal/camera/README.md](internal/camera/README.md) for details and known limitations (system-wide restart, won't fix a true USB-level hardware hang).

---

---

## USB Devices

```powershell
.\motorhome.exe usb                    # list every sim device and its state
.\motorhome.exe usb -v                 # ... with device instance IDs
.\motorhome.exe usb off pedals         # disable one device
.\motorhome.exe usb on pedals          # re-enable it
.\motorhome.exe usb toggle handbrake   # flip whichever way it currently is
.\motorhome.exe usb off all            # disable every connected sim device
```

Every device on the rig presents to Windows as a HID game controller. A game that enumerates all controllers and auto-binds axes picks up phantom input from the ones that aren't being used — harmless in a sim, which expects several devices, and disruptive in anything else. Disabling a device removes its HID node entirely, so nothing can bind to it until it's turned back on.

```
Sim racing USB devices

  Alias       State          Device
  handbrake   enabled        MOZA HBP Handbrake
  haptic      enabled        SIMAGIC P2000 Haptic
  pedals      enabled        Heusinkveld Sim Pedals Sprint
  wheelbase   not connected  SIMAGIC Alpha Series Wheelbase
```

Targets can be an alias, any unambiguous substring of a device name (`heusink` works), or `all`. A target matching several devices is an error listing them rather than a guess — disabling the wrong device mid-session is cheap to undo but not cheap to *notice*, since the symptom is a control that silently stopped working.

```
  [+] handbrake   MOZA HBP Handbrake               disabled
  [+] haptic      SIMAGIC P2000 Haptic             disabled
  [+] pedals      Heusinkveld Sim Pedals Sprint    disabled
  [=] wheelbase   SIMAGIC Alpha Series Wheelbase   not connected

A game that enumerated its controllers at startup may need restarting to notice.
```

A device already in the requested state reports `already disabled` rather than claiming a change that didn't happen, and one that isn't plugged in is reported and skipped rather than failing the command.

**Listing needs no special rights; changing a device state does.** The command re-runs itself elevated for that half only. UAC is set to never-notify on this machine, so this is silent — nothing pops up mid-session, and a Stream Deck button works the same as a terminal. Adding a device means one row in `KnownDevices` ([internal/usbdev/usbdev.go](internal/usbdev/usbdev.go)); find its VID/PID with:

```powershell
Get-PnpDevice -Class HIDClass -PresentOnly | Where-Object { $_.FriendlyName -match 'game controller' }
```

See [internal/usbdev/README.md](internal/usbdev/README.md) for why devices are matched by vendor/product ID rather than name, and why composite devices are toggled at the top-level node.

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

## Web Interface

```powershell
.\motorhome.exe gui                     # serve on 127.0.0.1:7777 and open a browser
.\motorhome.exe gui -port 8080          # a different port
.\motorhome.exe gui -no-open            # do not open a browser
```

Five panels:

| Panel | What it does |
|---|---|
| **Rig** | Start/stop/status of the configured apps, USB device toggles, camera restart |
| **Live** | Position, lap, and gaps to the cars ahead and behind, streamed at 2–30 Hz |
| **Sessions** | Pick an `.ibt` and render the full analysis — laps, sectors, phases, vs-PB deltas, exit impact, tyres, consistency, fuel, voice notes |
| **Personal bests** | Browse `pb.json`; open an entry for its setup, phase data and stored brake points |
| **Settings** | Edit `launcher.config.json` — driver, paths, hotkey, and the app list with reordering |

Runs in the foreground until Ctrl-C.

### It only listens on loopback

The interface can launch processes, rewrite your config and disable your pedals,
so it binds `127.0.0.1` and **there is no flag to change that**. It also requires
the `Host` header to be a loopback literal, which is what blocks DNS
rebinding — binding to 127.0.0.1 stops other machines connecting, but not a
remote page that points its own hostname at 127.0.0.1 and drives the API through
your browser.

This is not a login. It assumes one user on one machine, which is what a rig is.

### No dependencies, no build step

The page is plain HTML, CSS and JavaScript embedded in the binary with
`go:embed`, served by `net/http`. Nothing to install, nothing to compile, and
the zero-dependency property the rest of the tool has is unchanged.

### How it relates to the CLI

Nothing is reimplemented. Status and start/stop call the same `internal/launcher`
functions the CLI prints from; the analysis panel re-runs `motorhome analyze
-json` as a subprocess (so a bad lap number returns an error instead of killing
the server); USB toggles go through `motorhome usb`, which already handles the
UAC elevation; and the live panel builds its gaps with the same helper `motorhome
live` uses.

`coach` has no panel — the brief exists to be pasted into an AI assistant, and a
browser is not where that happens. Use `motorhome coach`.

See [internal/gui/README.md](internal/gui/README.md) for the design detail.

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
| `internal/analysis` | Lap extraction, phase-based segment stats, brake entry detection, sector times, consistency, note placement, setup diff | [README](internal/analysis/README.md) |
| `internal/trackmap` | GPS-based corner detection; `trackmap.json` store | [README](internal/trackmap/README.md) |
| `internal/pb` | Personal best tracking; `pb.json` store (managed via `motorhome pb`) | [README](internal/pb/README.md) |
| `internal/notes` | Voice note types and JSON persistence | [README](internal/notes/README.md) |
| `internal/iracing` | Live telemetry via iRacing shared memory | [README](internal/iracing/README.md) |
| `internal/audio` | Microphone recording via WinMM | [README](internal/audio/README.md) |
| `internal/camera` | Restarts the Windows Camera Frame Server | [README](internal/camera/README.md) |
| `internal/usbdev` | Identifies sim-racing USB devices and enables/disables them | [README](internal/usbdev/README.md) |
| `internal/gui` | Local web interface served by `motorhome gui` | [README](internal/gui/README.md) |

---

## Known Limitations

- `Minimized` window style not implemented — treated as `Normal`
- `stop` kills by image name — all instances killed if multiple are running
- `processName` whitespace not trimmed — accidental spaces cause silent failures
- `start`/`stop` always exit 0 even on partial failures
- Same-direction corner complexes (e.g. Maggotts/Becketts) not auto-merged; only direction-reversing chicanes are detected
- Segment names are auto-labelled T1/S1 etc — hand-edit `trackmap.json` for real corner names
- `gui` runs in the foreground until Ctrl-C — a Stream Deck button leaves a console window open; there is no tray icon or background mode
- `gui` is loopback-only with no authentication, so it serves one user on one machine — reaching it from a phone would need more than a bind-address change
- `gui` has no `coach` panel, no progress bar during an analysis, and no undo for the changes it makes
- The `gui` settings form takes typed paths — there is no file picker
