# cmd/motorhome

Main entry point and subcommand dispatch for the `motorhome` CLI.

## What it does

Parses the `-config` flag, loads the config file, and dispatches to one of ten subcommands: `start`, `stop`, `status`, `analyze`, `coach`, `pb`, `notes`, `live`, `camera`, `usb`.

## Files

| File | Contents |
|---|---|
| `main.go` | Flag parsing, config load, subcommand dispatch |
| `analyze.go` | `RunAnalyze` — the analyze subcommand's orchestration only |
| `analyze_out.go` | `analyzeOut` sink plus `aprintf`/`aprintln`/`aprint` — all analyze rendering goes through these so `-json` can discard it |
| `analyze_output.go` | All terminal rendering: `analyzeSingleLap`, setup tables, sector table, zone/phase/exit-impact/tyre/vs-PB/consistency tables, `runDump` |
| `analyze_notes.go` | Voice notes in analyze: `notesFileForIbt`, `loadNotesForIbt`, `locateSessionNotes`, `printNotes` |
| `analyze_json.go` | The `-json` wire format: `analyzeResult` and its `json*` types, `writeAnalyzeJSON` |
| `analyze_trace.go` | `-trace`/`-hz`: `buildSegmentTraces`, `printTraces`, and the large-trace stderr note |
| `analyze_json_build.go` | `buildAnalyzeResult` — maps computed values into the JSON document |
| `analyze_pb.go` | Stored-PB rendering for `-lap pb` (`runStoredPB*`, `printStoredPB`, `emitStoredPB`) and `phasesToPB` |
| `analyze_helpers.go` | Lap selection/filtering (`bestAnalyzeLap`, `flyingLapsWithinTime`, `crossLapComparableLaps`), `formatMapLine`, path and formatting helpers |
| `coach.go` | `RunCoach` — emits a self-contained coaching brief for an AI assistant |
| `coach_table.go` | `-table`: turn-by-turn table, flag thresholds, grading, sector loss |
| `coach_focus.go` | `-segment`: `focusOnSegments` narrows the brief to named corners |
| `coach_test.go` | Tests for brief structure, orientation gaps, trimming, framework loading |
| `coach_table_test.go` | Tests for per-corner aggregation, grading thresholds, table rendering |
| `coach_focus_test.go` | Tests for segment filtering, the focus disclosure, and trace rendering |
| `analyze_trace_test.go` | Tests for trace building, lap population, `-hz` and `printTraces` |
| `pb.go` | `RunPB` — the pb subcommand: `list`, `show`, `diff`, `prune` |
| `analyze_test.go` | Tests for lap selection and `.ibt` file resolution |
| `analyze_helpers_test.go` | Tests for `formatMapLine`, `pluralize`, `parseLapArg` |
| `analyze_helpers_extra_test.go` | Tests for `crossLapComparableLaps`, `selectAnalyzeLap`, `lapNumbers` and friends |
| `analyze_output_test.go` | Tests for every table printer and both `-dump` modes |
| `analyze_flow_test.go` | Tests for `analyzeSingleLap` and `resolveNotes` end to end |
| `analyze_notes_test.go` | Tests for notes file resolution, note/consistency rendering, and the JSON document |
| `analyze_json_build_test.go` | Tests for `buildAnalyzeResult` and the JSON section builders |
| `analyze_pb_render_test.go` | Tests for `phasesToPB` and the stored-PB renderers |
| `live_test.go` | Tests for the live gap/position formatters (Windows-tagged, like `live.go`) |
| `notes_paths_test.go` | Tests for session-file naming, whisper path resolution, clipboard |
| `pb_test.go` | Tests for pb entry ordering, filtering and stored-payload markers |
| `pb_cmd_test.go` | Tests for `pb list`/`show`/`prune`, including that a dry run writes nothing |
| `pb_exit_test.go` | Re-exec tests for the `os.Exit` refusal paths (see below) and for error attribution under `coach` |
| `clipboard.go` | `captureStdout` / `copyToClipboard` — tee analyze stdout into a buffer and pipe it into `clip.exe` (Windows) / `pbcopy` (macOS) |
| `clipboard_test.go` | Tests for stdout capture and restore |
| `notes.go` | `RunNotes` — notes subcommand: hotkey listen, record, transcribe, save |
| `notes_util_test.go` | Tests for key-name parsing and recent-`.ibt` lookup |
| `live.go` | `RunLive` — live position + gap to car ahead/behind from iRacing shared memory |
| `camera.go` | `RunCamera` — restart the Windows Camera Frame Server to clear a stuck webcam |
| `usb.go` | `RunUSB` — list and enable/disable the sim-racing USB devices |
| `elevate_windows.go` | `winIsElevated` / `winRelaunchElevated` — token check and `ShellExecuteExW runas` re-exec used by `usb` |
| `usb_test.go` | Tests for usb listing, toggling, absent devices and the elevation hand-off |

## Dispatch

```
motorhome [-config <path>] <subcommand> [args]

start / stop / status  →  internal/launcher
analyze [flags] [file] →  RunAnalyze in analyze.go
coach [flags] [file]   →  RunCoach in coach.go
pb [list|show|diff|prune] →  RunPB in pb.go
notes [set-hotkey]     →  RunNotes in notes.go
live [-watch] [-hz N]  →  RunLive in live.go
camera                 →  RunCamera in camera.go
usb [on|off|toggle]    →  RunUSB in usb.go
```

Runtime file paths are all derived from the config file's directory:
- `trackmap.json` — segment geometry store
- `pb.json` — personal best store
- `notes/` — voice notes directory

`analyze.go` was ~1150 lines and its package sat at ~10% coverage; the rendering and helper code is now split out so it can be tested directly. `formatMapLine` is the first result of that — the map-summary line used to be inline in `RunAnalyze` and therefore untestable.

## Testing this package

Coverage is ~58%. The uncovered remainder is deliberate, not a backlog: it is the subcommand entry points (`main`, `RunAnalyze`, `RunLive`, `RunNotes`, `RunCamera`), the Win32 keyboard hooks and beeps, `transcribeLocal` (shells out to whisper), and `runPBDiff` (needs a real `.ibt`, which the repo gitignores). Everything reachable without hardware, a live sim, or a process boundary is covered.

Two mechanisms make that possible:

- **The `analyzeOut` sink.** Tests point it at a `bytes.Buffer` via `captureAnalyzeOut`, so every table printer is assertable. Before the sink existed these functions wrote to `os.Stdout` directly and could only be tested by capturing the process's stdout.
- **`captureStdout`.** The clipboard helper doubles as a test tool for the printers that still use `fmt.Print*` directly (`live.go`, `pb.go`). Note it *tees* — output still reaches the terminal, so a verbose test run is noisy by design.

`pb_exit_test.go` uses the re-exec pattern: `TestMain` checks `MOTORHOME_EXIT_CASE` and, when set, runs one named case that ends in `os.Exit` instead of the normal suite. The parent then asserts on exit code and stderr. This covers the "refuses to guess" contracts — prune with no criteria, an ambiguous `show` filter, an unknown subcommand — which are the safety behaviour of a destructive command and worth pinning even though they cost a subprocess.

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

Flags: `-lap N|pb`, `-update-map`, `-geo-method latlon|lataccel`, `-dump <seg>`, `-dump-all`, `-trace <segs>`, `-hz N`, `-json`, `-note-lag <seconds>`

### `-trace` and `-hz` (`analyze_trace.go`)

`-trace T3,T4` prints sample-level telemetry for named segments inline, where `-dump` writes it to a CSV file. The two are not redundant: a file is for plotting or handing to something else, and inline rows are what let `coach -segment` put a corner's samples in front of the assistant doing the coaching. Both share `DumpConfig` and `writeDumpRows`, so the numbers and formatting are identical.

Traces are built once in `RunAnalyze` and rendered twice — through `printTraces` to the table sink for a human, and into the JSON document's `traces` field for `coach`. In `-json` mode the sink is `io.Discard`, so the terminal rendering costs nothing there.

They cover **every comparable lap**, not just the analysed one. Comparing laps against each other is the point of zooming in: one lap's trace shows what the driver did, not which of the things they did was the one that varied. A session with no comparable set falls back to the analysed lap alone.

`-hz N` sets the output rate for both `-dump` and `-trace`, defaulting to each path's own default (20Hz for dump, 60Hz for trace). It is rejected without one of them rather than silently ignored. Above `traceRowsWarnThreshold` (2000) rows a stderr note reports the size and points at `-hz 20` — 60Hz is the right default for a focused trace, but the cost is invisible until something downstream chokes on it, and since binary channels are now aggregated rather than decimated, dropping to 20Hz costs resolution on the continuous traces and nothing else.

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

## coach subcommand (`coach.go`)

`RunCoach(args, cfg, trackmapPath, pbPath, notesDir, cfgPath)` emits one self-contained markdown brief for an AI assistant: session orientation → the embedded `coach.md` framework → the analysis as JSON.

It exists so coaching is one command rather than a procedure. The assistant used to run `analyze`, separately read `coach.md`, and reconcile an ASCII table against a framework written for a human; bundling the three removes the dependency on the assistant's working directory and on it remembering the steps.

**No network call and no API key** — the assistant reading the brief is the coach. That was chosen over calling the Anthropic API to keep the repo's zero-external-dependency property.

Implementation notes:

- **Reuses the analyze pipeline wholesale.** `RunAnalyze` is called in-process with `-json` and its stdout captured. A second analysis path would be free to drift from what `analyze` reports.
- **`captureStdoutSilent`** is the non-teeing sibling of `captureStdout`. The clipboard capture deliberately tees to the terminal; coach must not, or the raw JSON prints above the brief.
- **`invokedAs`** names the subcommand the user actually typed, so failures inside the shared pipeline attribute themselves correctly. `RunCoach` sets it to `coachInvocation` before calling in; without that, `coach -lap 99` reports `analyze: lap 99 not found` and `coach -segment T99` cites `-trace` — a flag that is not on the command the user ran. It also carries `traceFlag` (`-trace` vs `-segment`) for the same reason. A package var rather than a threaded parameter, for the same reason `analyzeOut` is one: the die paths are scattered across the pipeline. `dieMessage` is split out from `analyzeDie`/`coachDie` so the wording can be asserted without re-execing the test binary.
- **`trimForCoaching`** drops the track map's raw segment geometry — no coaching signal, since every segment is already named in the phase rows — replacing it with `SegmentCount`. Map confidence and match score are kept: a low-confidence map means the boundaries are suspect and findings pinned to them need hedging. Nothing else is trimmed.
- **The orientation names what is missing** in a `Gaps:` line. Coaching around an absent track map or PB without realising it is missing produces confident findings about nothing.
- A missing `coach.md` warns on stderr and is skipped rather than being fatal; `-no-framework` omits it deliberately.

### `-segment` — focusing on named corners (`coach_focus.go`)

`coach -segment T3` narrows the brief to one or more corners and inlines their samples. The aggregate rows the brief normally carries answer *which* corner is costing time; they cannot answer *what is happening in it*, because a mean and a standard deviation describe a corner rather than replaying it. Focusing swaps breadth for depth: per-segment rows for everything else come out, and the segment's actual telemetry (via `analyze -trace`) goes in as fenced CSV blocks after the JSON.

`focusOnSegments` filters only per-segment collections — phases, vs-PB deltas, exit impact, consistency. Session-level content stays whole: the lap list, sector times, fuel, the PB header and the voice notes are small, they are the context that makes a single corner interpretable, and a sector time is the one thing that says whether the focused corner is where the time is actually going. Segment geometry is kept too, because `-table` needs it to tell a corner from a straight.

**The trade is only safe if the reader knows it was made.** A narrowed brief is otherwise indistinguishable from a whole-session one, so `focusOnSegments` sets `Focus` and `writeCoachOrientation` turns it into a bolded line stating that other corners were removed and that the lap must not be characterised as a whole. Without it the assistant would confidently report the session's main problem having been shown one corner of it — the same failure mode the `Gaps:` line exists to prevent.

Traces travel outside the JSON document rather than inside it: they are already CSV, and nesting them in an indent-encoded object would cost tokens and readability for nothing. `-hz N` sets their rate (default 60). `-table` accepts `-segment` but not the traces — a 60Hz CSV is not something a human scans between runs, and the table has no column for it.

### `-table` — the turn-by-turn view (`coach_table.go`)

`coach -table` prints one row per corner instead of the AI brief: `Turn | Speed in>min>out | Coast | Lock | Spin | ExitSD | Flags | Grade`, followed by a sector-loss table and a ranking of the least repeatable exits. It is a second renderer over the same `analyzeResult`, for the same reason `analyze_json.go` exists alongside the ASCII tables — the brief is written for an assistant to read, this is the same content collapsed for a human to scan between runs.

- **Corners only.** Straights are dropped: a straight is either flat or a segment-boundary artifact, and including them would double the table. Braking that begins on the straight before a corner is therefore *not* charged to that corner — the legend says so and points at the phase table.
- **The Grade is a triage hint, not a measurement.** The tool has no model of a well-driven corner. The letter counts how many of five fixed thresholds (`coachCoastFlagSeconds` and friends) a corner trips: 0 flags = A, 4+ = F. The thresholds are heuristics and are printed in the legend beneath the table, so a grade can be argued with rather than taken on faith.
- **No "Fix" column.** Deciding what to do about a flagged corner is the coaching judgement the assistant supplies; generated from thresholds it would be a canned string dressed up as advice.
- **Spread needs two laps.** `ExitSD` renders `-` and cannot trip the spread flag below two comparable laps — a one-lap corner has no spread, and grading it clean would be a lie of omission. A footer states how many corners are in that position.
- **Aggregation is worst-phase, not mean**, for `ExitSD`: one badly repeated phase is the finding, and averaging it against two steady ones hides it. Coast/lock/spin/corrections are summed across the corner's phases.
- **Renders from the untrimmed result.** `buildCoachTableView` runs before `trimForCoaching`, because classifying a segment as a corner needs the track map's `Kind` — exactly the geometry the brief drops. `cornerSegmentNames` falls back to the phase name (`full` = straight) when the geometry is gone, which is why the table still works on a trimmed result; that fallback misclassifies corners taken under 5° of steering, so it is the fallback and not the rule.
- **Sector loss is self-vs-self.** Every entry in the `Best` column was set during the same session, so the total is time the driver has already demonstrated rather than a simulated ideal. It is coarse — it names a third of the lap, not a corner — which is why it sits under the turn table instead of replacing it.

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

## usb subcommand (`usb.go`)

`RunUSB(args) int` lists the sim-racing USB devices and enables or disables
them. Returns the process exit code rather than calling `os.Exit`, so every path
is testable in-process.

```
motorhome usb                              # list, with state
motorhome usb -v                           # list, with device instance IDs
motorhome usb <on|off|toggle> <alias|all>  # change state
```

Device identification, matching and target resolution all live in
[internal/usbdev](../../internal/usbdev/README.md). This file owns argument
parsing, output, and the elevation hand-off.

### Elevation

Enumerating devices needs no special rights. **Changing one does.** On this
machine the user is in the Administrators group but normal processes still run
with the filtered token, so `winIsElevated` checks `TokenElevation` rather than
group membership — a membership check would report true for a process that
cannot change a device state.

When a state change is requested unelevated, `usbElevate` re-runs the exe via
`ShellExecuteExW` with the `runas` verb (`elevate_windows.go`) and waits for it.
UAC is set to never-notify here, so this is silent — no dialog interrupts a
Stream Deck press mid-session.

The elevated child gets its own console that the parent has no handle to, so it
cannot print to the terminal. `-elevated-out <path>` redirects both `usbOut` and
`usbErrOut` to a file; the parent reads it back and replays it, making a toggle
look identical whether or not it had to elevate. That flag is also the recursion
guard: a process started with it never elevates again, so a failed elevation
produces one clear "access denied" rather than a loop.

### What runs where

Enumeration and target resolution stay in the **unelevated parent**, so a typo
(`no device matches "pedls"`) is reported without paying for an elevation first.
`usbApply` runs only in the process holding the token, and owns *every* line of
per-device output — printing in both would show each device twice, once from the
parent and once replayed from the child.

`-v` and `-elevated-out` are rebuilt into the child's argv rather than forwarded
verbatim, so flags land ahead of the target regardless of how they were typed.

### Reporting

A device already in the requested state prints `already disabled` rather than
claiming a change that didn't happen; an unplugged one prints `not connected`
and is skipped instead of failing the command, so `usb off all` still works with
the wheelbase off the rig. A target that is *entirely* unplugged is an error,
checked before elevating.
