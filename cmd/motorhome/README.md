# cmd/motorhome

Main entry point and subcommand dispatch for the `motorhome` CLI.

## What it does

Parses the `-config` flag, loads the config file, and dispatches to one of eight subcommands: `start`, `stop`, `status`, `analyze`, `pb`, `notes`, `live`, `camera`.

## Files

| File | Contents |
|---|---|
| `main.go` | Flag parsing, config load, subcommand dispatch |
| `analyze.go` | `RunAnalyze` — the analyze subcommand's orchestration only |
| `analyze_out.go` | `analyzeOut` sink plus `aprintf`/`aprintln`/`aprint` — all analyze rendering goes through these so `-json` can discard it |
| `analyze_output.go` | All terminal rendering: `analyzeSingleLap`, setup tables, sector table, zone/phase/exit-impact/tyre/vs-PB/consistency tables, `runDump` |
| `analyze_notes.go` | Voice notes in analyze: `notesFileForIbt`, `loadNotesForIbt`, `locateSessionNotes`, `printNotes` |
| `analyze_json.go` | The `-json` wire format: `analyzeResult` and its `json*` types, `writeAnalyzeJSON` |
| `analyze_json_build.go` | `buildAnalyzeResult` — maps computed values into the JSON document |
| `analyze_pb.go` | Stored-PB rendering for `-lap pb` (`runStoredPB*`, `printStoredPB`, `emitStoredPB`) and `phasesToPB` |
| `analyze_helpers.go` | Lap selection/filtering (`bestAnalyzeLap`, `flyingLapsWithinTime`, `crossLapComparableLaps`), `formatMapLine`, path and formatting helpers |
| `pb.go` | `RunPB` — the pb subcommand: `list`, `show`, `diff`, `prune` |
| `analyze_test.go` | Tests for lap selection and `.ibt` file resolution |
| `analyze_helpers_test.go` | Tests for `formatMapLine`, `pluralize`, `parseLapArg` |
| `analyze_notes_test.go` | Tests for notes file resolution, note/consistency rendering, and the JSON document |
| `pb_test.go` | Tests for pb entry ordering, filtering and stored-payload markers |
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
pb [list|show|diff|prune] →  RunPB in pb.go
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

`RunAnalyze(args, cfg, trackmapPath, pbPath, notesDir)` flow:
1. Resolve `.ibt` path (explicit, numeric index, or most-recent from `ibtDir`)
2. Open file, extract laps and session metadata
3. Find best flying lap; filter flying laps to within 1.5s of best time (drops slow early-practice laps)
4. Load trackmap; detect from filtered laps if no entry exists (latlon, fallback to lataccel)
5. Compute match score; compute/blend brake entries on new sessions
6. Update geometry counters; save trackmap
7. Load pb.json; update PB if new; save
8. Compute the cross-lap comparable set, consistency rows, and located voice notes
9. Print header, lap list, sector table, per-lap tables, consistency and notes — or emit the JSON document

Stdout is wrapped by `captureStdout` in `main.go` so the full analyze output is teed into a buffer while still streaming to the terminal; after `RunAnalyze` returns, the buffer is piped into `clip.exe` (Windows) / `pbcopy` (macOS) so the user can paste it straight into Claude. `analyzeDie` calls `os.Exit(1)` which skips the deferred clipboard write — partial broken output is intentionally not copied.

Flags: `-lap N|pb`, `-update-map`, `-geo-method latlon|lataccel`, `-dump <seg>`, `-dump-all`, `-json`, `-note-lag <seconds>`

### Output sink and `-json`

Every table goes through `aprintf`/`aprintln`/`aprint` (`analyze_out.go`) rather than `fmt.Print*`, so the rendering stage can be redirected. `-json` points it at `io.Discard` and writes the JSON document to stdout instead; tests point it at a buffer, which is what makes the printers testable at all.

`analyzeOut` is bound to `os.Stdout` at the **start of `RunAnalyze`**, not at package init: `main.go` swaps `os.Stdout` for the clipboard pipe before calling in, and a package-level initialiser would capture the pre-swap descriptor and bypass the clipboard entirely.

Warnings and errors deliberately bypass the sink and go straight to stderr, so they still reach the user in `-json` mode without corrupting the document on stdout.

### Two lap populations

There are two different filtered lap sets, and conflating them was the first thing that went wrong when consistency was added:

- `flyingLapsWithinTime` — within **1.5s** of the best lap. Feeds trackmap detection and brake-entry blending, where one sloppy lap genuinely corrupts corner geometry.
- `crossLapComparableLaps` — within **10%** of the best lap. Feeds the consistency table and `-dump-all`. Spread is the opposite problem: filtering down to the laps that were already alike reports "perfectly consistent" for a session that was not, and in practice the 1.5s window often leaves a single lap, which has no spread at all. A percentage rather than a fixed delta so it scales with lap length.

The consistency header names the contributing lap numbers, because the population is not the same as the lap list printed above it.

### Voice notes (`analyze_notes.go`)

The notes subcommand names each session file after the `.ibt` it detected, so the join is by filename: `<notesDir>/<ibt basename>.json`. A missing notes file is the normal case and is not an error; a file that exists but cannot be parsed produces a stderr warning and is skipped, so a broken notes file never takes down an otherwise complete telemetry analysis.

## pb subcommand (`pb.go`)

`RunPB(args, cfg, pbPath)` — inspects, compares and prunes the personal-best store. `pb.json` accumulates a record per car/track and is never trimmed by the analyze flow, so this is the only place entries can be read in bulk or removed.

| Command | Behaviour |
|---|---|
| `pb list [filter]` | Table of every stored PB: car, track, time, date, and which optional payloads it carries |
| `pb show <filter>` | Full record — setup tables and phase table — for exactly one matching entry |
| `pb diff [file.ibt]` | Setup differences between a session and that car/track's stored PB setup |
| `pb prune -older-than N \| -match F [-apply]` | Remove entries; previews unless `-apply` is given |

`<filter>` is a case-insensitive substring matched against `"car | track"`. `show` and `prune` refuse to guess: a filter matching zero or several entries lists the candidates and exits non-zero.

Entries are sorted by track then car for output, since Go map order is random and listings would otherwise shuffle between runs.

**Prune is preview-by-default.** Deleting a PB throws away accumulated brake-entry positions and the setup that produced the lap, none of which can be recovered from telemetry that may itself be long deleted — so the destructive path has to be asked for with `-apply`. Prune also refuses to run with no criteria rather than selecting everything, and reports (rather than silently skipping) entries whose date cannot be parsed.

**`pb diff` hides session state by default.** iRacing writes end-of-session tyre readings back into the `CarSetup` block, so a raw diff of two identical setups still reports every corner's hot pressure, temperature and tread. Those are filtered out and counted; `-all` shows them. On a real comparison this cut 29 rows to 16 genuine setup changes.

## notes subcommand (`notes.go`)

`RunNotes(args, cfg, notesDir, cfgPath)` flow:
1. `set-hotkey` arg: install keyboard + Raw Input hooks, save first key pressed to config, exit
2. Otherwise: start `recordingWorker` goroutine, install hotkey hook
3. First press: play start chime (A5→C6), start `audio.Recorder`
4. Second press: stop `audio.Recorder`, play stop chime (E5→A4), write `.wav` to temp file
5. Shell out to `whisper-cli` for transcription; parse stdout
6. Append `Note{StartedAt, Timestamp, Text}` to session file; print `[note] transcribed text`

`StartedAt` is captured when recording begins, not when it stops. `analyze` places notes on track by wall clock, and the stop time trails the event being described by the whole length of the utterance — several hundred metres at racing speed.

Toggle model: a single `toggleCh chan struct{}` is sent on every key-down or HID button-down. `recordingWorker` alternates between idle and recording on each message. Key-up and button-release events are ignored.

Beeps use `kernel32.Beep`. Start: 880 Hz (A5, 80ms) + 1047 Hz (C6, 100ms). Stop: 659 Hz (E5, 80ms) + 440 Hz (A4, 120ms). All tones are from the A harmonic family — musically consistent.

Session file is named after the most recently modified `.ibt` in `ibtDir` (within 4 hours), falling back to a plain timestamp.
