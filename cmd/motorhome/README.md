# cmd/motorhome

Main entry point and subcommand dispatch for the `motorhome` CLI.

## What it does

Parses the `-config` flag, loads the config file, and dispatches to one of five subcommands: `start`, `stop`, `status`, `analyze`, `notes`.

## Files

| File | Contents |
|---|---|
| `main.go` | Flag parsing, config load, subcommand dispatch |
| `analyze.go` | `RunAnalyze` — full analyze subcommand implementation |
| `analyze_test.go` | Tests for analyze output formatting |
| `notes.go` | `RunNotes` — notes subcommand: hotkey listen, record, transcribe, save |

## Dispatch

```
motorhome [-config <path>] <subcommand> [args]

start / stop / status  →  internal/launcher
analyze [flags] [file] →  RunAnalyze in analyze.go
notes [set-hotkey]     →  RunNotes in notes.go
```

Runtime file paths are all derived from the config file's directory:
- `trackmap.json` — segment geometry store
- `pb.json` — personal best store
- `notes/` — voice notes directory

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
