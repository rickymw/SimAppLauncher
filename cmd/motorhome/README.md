# cmd/motorhome

Main entry point and subcommand dispatch for the `motorhome` CLI.

## What it does

Parses the `-config` flag, loads the config file, and dispatches to one of seven subcommands: `start`, `stop`, `status`, `analyze`, `notes`, `live`, `camera`.

## Files

| File | Contents |
|---|---|
| `main.go` | Flag parsing, config load, subcommand dispatch |
| `analyze.go` | `RunAnalyze` — the analyze subcommand's orchestration only |
| `analyze_output.go` | All terminal rendering: `analyzeSingleLap`, setup tables, zone/phase/exit-impact/tyre/vs-PB tables |
| `analyze_pb.go` | Stored-PB rendering for `-lap pb` (`runStoredPB*`, `printStoredPB`) and `phasesToPB` |
| `analyze_helpers.go` | Lap selection/filtering (`bestAnalyzeLap`, `flyingLapsWithinTime`), `formatMapLine`, path and formatting helpers |
| `analyze_test.go` | Tests for lap selection and `.ibt` file resolution |
| `analyze_helpers_test.go` | Tests for `formatMapLine`, `pluralize`, `parseLapArg` |
| `clipboard.go` | `captureStdout` / `copyToClipboard` — tee analyze stdout into a buffer and pipe it into `clip.exe` (Windows) / `pbcopy` (macOS) |
| `clipboard_test.go` | Tests for stdout capture and restore |
| `notes.go` | `RunNotes` — notes subcommand: hotkey listen, record, transcribe, save |
| `notes_util_test.go` | Tests for key-name parsing and recent-`.ibt` lookup |
| `live.go` | `RunLive` — live position + gap to car ahead/behind from iRacing shared memory |
| `camera.go` | `RunCamera` — restart the Windows Camera Frame Server to clear a stuck webcam |

## Dispatch

```
motorhome [-config <path>] <subcommand> [args]

start / stop / status  →  internal/launcher
analyze [flags] [file] →  RunAnalyze in analyze.go
notes [set-hotkey]     →  RunNotes in notes.go
live [-watch] [-hz N]  →  RunLive in live.go
camera                 →  RunCamera in camera.go
```

Runtime file paths are all derived from the config file's directory:
- `trackmap.json` — segment geometry store
- `pb.json` — personal best store
- `notes/` — voice notes directory

`analyze.go` was ~1150 lines and its package sat at ~10% coverage; the rendering and helper code is now split out so it can be tested directly. `formatMapLine` is the first result of that — the map-summary line used to be inline in `RunAnalyze` and therefore untestable.

## analyze subcommand (`analyze.go`)

`RunAnalyze(args, cfg, trackmapPath, pbPath)` flow:
1. Resolve `.ibt` path (explicit, numeric index, or most-recent from `ibtDir`)
2. Open file, extract laps and session metadata
3. Find best flying lap; filter flying laps to within 1.5s of best time (drops slow early-practice laps)
4. Load trackmap; detect from filtered laps if no entry exists (latlon, fallback to lataccel)
5. Compute match score; compute/blend brake entries on new sessions
6. Update geometry counters; save trackmap
7. Load pb.json; update PB if new; save
8. Print header, lap list, segment table or comparison table

Stdout is wrapped by `captureStdout` in `main.go` so the full analyze output is teed into a buffer while still streaming to the terminal; after `RunAnalyze` returns, the buffer is piped into `clip.exe` (Windows) / `pbcopy` (macOS) so the user can paste it straight into Claude. `analyzeDie` calls `os.Exit(1)` which skips the deferred clipboard write — partial broken output is intentionally not copied.

Flags: `-lap N`, `-compare N,M`, `-update-map`, `-geo-method latlon|lataccel`

## notes subcommand (`notes.go`)

`RunNotes(args, cfg, notesDir, cfgPath)` flow:
1. `set-hotkey` arg: install keyboard + Raw Input hooks, save first key pressed to config, exit
2. Otherwise: start `recordingWorker` goroutine, install hotkey hook
3. First press: play start chime (A5→C6), start `audio.Recorder`
4. Second press: stop `audio.Recorder`, play stop chime (E5→A4), write `.wav` to temp file
5. Shell out to `whisper-cli` for transcription; parse stdout
6. Append `Note{Timestamp, Text}` to session file; print `[note] transcribed text`

Toggle model: a single `toggleCh chan struct{}` is sent on every key-down or HID button-down. `recordingWorker` alternates between idle and recording on each message. Key-up and button-release events are ignored.

Beeps use `kernel32.Beep`. Start: 880 Hz (A5, 80ms) + 1047 Hz (C6, 100ms). Stop: 659 Hz (E5, 80ms) + 440 Hz (A4, 120ms). All tones are from the A harmonic family — musically consistent.

Session file is named after the most recently modified `.ibt` in `ibtDir` (within 4 hours), falling back to a plain timestamp.
