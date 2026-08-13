# CLAUDE.md — MotorHome

## Project overview
Windows CLI tool (`motorhome.exe`) that launches, monitors, and closes sim racing apps in sequence, analyses iRacing `.ibt` telemetry files, and records voice notes during a session. Designed for Stream Deck integration. Nine subcommands: `start`, `stop`, `status`, `analyze`, `coach`, `pb`, `notes`, `live`, `camera`. Accepts an optional `-config <path>` flag.

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

### Testing the cmd package
`cmd/motorhome` sits at ~58% coverage; `internal/analysis` at ~92%. The uncovered
remainder in `cmd` is deliberate: subcommand entry points, Win32 keyboard
hooks/beeps, `transcribeLocal`, and `runPBDiff` (needs a real `.ibt`, which is
gitignored). Two mechanisms make the rest testable:
- **`analyzeOut`** (`analyze_out.go`) — point it at a buffer with
  `captureAnalyzeOut` to assert on any analyze table.
- **`captureStdout`** (`clipboard.go`) — for printers that still use `fmt.Print*`
  directly (`live.go`, `pb.go`). It *tees*, so verbose test runs are noisy.

`pb_exit_test.go` covers the `os.Exit` refusal paths by re-execing the test
binary: `TestMain` dispatches on `MOTORHOME_EXIT_CASE`. Use that pattern when
adding a new "refuses to guess" behaviour rather than leaving it untested.

## Commit rule
After completing a feature, bug fix, or any other coherent code change, commit and push to `origin` without waiting for explicit permission. The repo is single-developer, master-only — every change should land. Stage just the files relevant to the change (do not bundle unrelated work-in-progress) and write a focused conventional-style commit message.

## Formatting rule
Run `gofmt -w` on every file you touch, before committing. The whole tree was
brought to `gofmt` compliance on 2026-07-28 (22 files were drifting); keep it
that way so `gofmt -l .` stays empty and formatting noise never mixes into a
behavioural diff.

Note that `go vet ./...` does **not** pass cleanly and cannot be used as a gate:
three `unsafe.Pointer` conversions in `cmd/motorhome/notes.go` and
`internal/iracing/live_windows.go` are deliberate and correct (OS-owned memory
from Win32 callbacks and `MapViewOfFile`). Their `//nolint:govet` comments are
golangci-lint syntax and do not suppress `go vet`.

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
motorhome analyze -lap pb                           # render stored PB lap from pb.json
motorhome analyze -update-map session.ibt           # re-detect track segments from this session
motorhome analyze -geo-method lataccel session.ibt  # use lateral G instead of GPS curvature
motorhome analyze -dump T3 session.ibt              # dump T3 telemetry to CSV for AI analysis
motorhome analyze -dump 5 -lap 3 session.ibt        # dump 5th segment from lap 3
motorhome analyze -dump T3 -dump-all session.ibt    # dump T3 from every comparable lap into one CSV
motorhome analyze -trace T3,T4                      # print T3/T4 sample telemetry inline (60Hz)
motorhome analyze -trace T3 -hz 20                  # same, downsampled to 20Hz
motorhome analyze -json session.ibt                 # emit the whole analysis as JSON instead of tables
motorhome analyze -note-lag 3 session.ibt           # shift voice notes 3s earlier when placing them
motorhome analyze -fuel-laps 12                     # litres needed to complete 12 more laps
motorhome coach                                     # self-contained coaching brief for an AI assistant
motorhome coach -lap 3                              # coach a specific lap
motorhome coach -no-framework                       # data only, without the embedded framework
motorhome coach -table                              # turn-by-turn table for a human, not the AI brief
motorhome coach -segment T3                         # focus on one corner, with its sample-level telemetry
motorhome coach -segment T3,T4 -hz 20               # two corners, traces downsampled to 20Hz
motorhome pb                                        # list every stored personal best
motorhome pb show watkins                           # full record (setup + phases) for one entry
motorhome pb diff                                   # setup changes since the PB for this session's car/track
motorhome pb prune -older-than 180                  # preview removal of stale entries (-apply to write)
motorhome live                                      # one-shot position + gap to car ahead/behind
motorhome live -watch                               # stream updates at 5 Hz until Ctrl-C
motorhome live -watch -hz 10                        # poll at 10 Hz
motorhome live -raw                                 # dump raw LiveData fields (diagnostic)
motorhome camera                                    # restart a stuck/frozen webcam
```

## AI Coaching workflow
When the user asks to be coached, to analyse their session, or to review a lap, use Bash to run the coach command — do not ask them to paste output.

```powershell
.\motorhome.exe coach              # most recent session
.\motorhome.exe coach -lap 3       # a specific lap
.\motorhome.exe coach session.ibt  # a specific file
```

That emits one self-contained brief: session orientation (including a `Gaps:` line naming what is missing), the full `coach.md` framework inline, and the analysis as JSON. **There is no second step — do not separately read `coach.md` or run `analyze`.** Deliver per-segment findings and a **Top 3 Actions** list.

When a finding needs the samples rather than the aggregates — pedal timing, where the brake release actually happens, which lap's throttle came in late — re-run focused on that corner:

```powershell
.\motorhome.exe coach -segment T3        # or T3,T4
.\motorhome.exe coach -segment T3 -hz 20 # if the trace comes back too large
```

The focused brief carries a `Focus:` line: everything outside those corners has been removed, so do not characterise the lap as a whole or rank the focused corners against the rest from it. Reach for this *after* the full brief has identified the corner, not instead of it.

`analyze` remains the human-facing view (formatted tables, auto-copied to the clipboard); `coach` is the machine-facing one. There is no API key and no network call — the assistant reading the brief *is* the coach.

## Architecture

Each package has its own README with full detail. Below is a terse summary with links.

| Package | Role | Details |
|---|---|---|
| `cmd/motorhome` | Entry point, flag parsing, subcommand dispatch (`analyze.go`, `coach.go`, `pb.go`, `notes.go`) | [README](cmd/motorhome/README.md) |
| `internal/config` | `Config`/`App` structs, JSON load, `Validate()` | [README](internal/config/README.md) |
| `internal/launcher` | `ProcessManager` interface; `RunStart`/`RunStop`/`RunStatus`; `tasklist`/`taskkill`; `SeDebugPrivilege` fallback | [README](internal/launcher/README.md) |
| `internal/ibt` | Low-level `.ibt` binary parser; `File.Sample(i)` typed accessor | [README](internal/ibt/README.md) |
| `internal/analysis` | `ExtractLaps`, `ComputePhases`, `ComputeBrakeEntries`, `ComputeTyreSummary`, `ComputeConsistency`, `ComputeFuel`, `LocateNotes`, `DumpSegmentCSV`/`DumpSegmentAllLapsCSV`, `FlattenSetup`/`DiffSetups`, `ParseSessionMeta`, `ParseSectors`/`ComputeSectorTimes` | [README](internal/analysis/README.md) |
| `internal/trackmap` | GPS curvature corner detection (`latlon`) with steering/speed/lat-G validation; fallback `lataccel`; `trackmap.json` load/save | [README](internal/trackmap/README.md) |
| `internal/pb` | Personal best store; `pb.Update` returns true on new PB; `PBPhase` stores per-segment data for delta comparison | [README](internal/pb/README.md) |
| `internal/notes` | `Note{Timestamp,Text}`/`Session` types; `AppendNote` load→append→save | [README](internal/notes/README.md) |
| `internal/iracing` | `ReadLiveData()` snapshot from iRacing shared memory (Windows-only); `ParseDrivers`, `ComputeGaps` for gap-to-car math (cross-platform) | [README](internal/iracing/README.md) |
| `internal/audio` | WinMM `Recorder.Start/Stop`; `BuildWAV` for Whisper input | [README](internal/audio/README.md) |
| `internal/camera` | `Restarter` interface; Windows impl stops/starts `FrameServer`+`FrameServerMonitor` via raw SCM syscalls to un-stick a frozen webcam | [README](internal/camera/README.md) |

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
3. Find best flying lap; filter flying laps to within 1.5s of best lap time (drops slow early-practice laps). Both best-lap selection and the within-time filter also reject laps shorter than 70% of the session's median flying-lap time (`plausibleLapMinTime`) — guards against a stitched/phantom `LapLastLapTime` value that iRacing occasionally publishes (e.g. after a session reset/recording gap), which would otherwise be picked as a sub-real "best lap" and corrupt the trackmap and PB. Floor only applies with 2+ flying laps. Laps flagged `IsCut` (gap in LapDistPct coverage — see Cut lap detection below) are also rejected here
4. Load `trackmap.json`; detect from filtered laps if no entry exists (latlon → lataccel fallback)
5. Compute match score (always lataccel for consistency); compute/blend `brakeEntryPct` on new sessions using filtered laps
6. Increment `lapsUsed`/`sessionsUsed` once per unique session; save trackmap
7. Load `pb.json`; capture the existing entry's `Phases` into a local `pbPhases` (used later by the vs-PB delta table) *before* mutating the entry; update if new PB; if new PB and segments available, store phase data (`PBPhase`) and the raw `CarSetup:` YAML block (`Setup` field) for the PB lap; save

   `pb.Update` replaces the entry wholesale and preserves only `BrakeEntries` — the previous PB's `Phases`/`Setup` are dropped. That is deliberate: they describe a different, slower lap, and pairing them with the new lap time would make the record self-inconsistent. When a new PB is set with no track map available (so no replacement phases can be computed), a warning is printed to stderr, because the silent consequence is that the *next* session has no vs-PB table.
8. Print: header (file, driver, car, track) → setup tables (Tyres + Suspension corners parsed from CarSetup YAML) → tyre summary (avg surface temps, end-of-lap wear, hot pressures, brake bias) → map line → PB line → lap list → sector table → phase table → vs PB delta table (if stored PB phases exist) → corner exit → straight peak table → consistency table → notes table → segment traces (only with `-trace`)

### Two lap populations
Two different filtered lap sets exist and must not be conflated:
- `flyingLapsWithinTime` — within **1.5s** (`lapTimeFilterDelta`) of the best lap. Feeds trackmap detection and brake-entry blending, where one sloppy lap genuinely corrupts corner geometry.
- `crossLapComparableLaps` — within **10%** (`consistencyLapFilterPct`) of the best lap. Feeds the consistency table and `-dump-all`.

Spread is the opposite problem from geometry: filtering down to the laps that were already alike reports "perfectly consistent" for a session that was not, and in practice the 1.5s window often leaves a *single* lap, which has no spread at all (observed on a real 3-flying-lap session where the driver was still improving). A percentage rather than a fixed delta so it scales with lap length.

### analyze output sink and `-json`
Every analyze table is written through `aprintf`/`aprintln`/`aprint` (`cmd/motorhome/analyze_out.go`), not `fmt.Print*`. `-json` points the sink at `io.Discard` and writes the JSON document to stdout instead; tests point it at a buffer, which is what makes the printers testable.

`analyzeOut` is bound to `os.Stdout` at the **start of `RunAnalyze`**, not at package init — `main.go` swaps `os.Stdout` for the clipboard pipe before calling in, and a package-level initialiser would capture the pre-swap descriptor and bypass the clipboard.

Warnings and errors deliberately bypass the sink and go straight to stderr, so they still reach the user in `-json` mode without corrupting the document on stdout.

The JSON document (`analyze_json.go`) is versioned by `analyzeSchema` (`motorhome.analyze/1.1`) and uses its own `json*` types rather than marshalling the internal analysis structs — those are free to be renamed as internals, this is a published wire format. `-lap pb -json` emits the stored `PersonalBest` record alone, since there is no session to describe alongside it.

### Consistency table
`Name | Phase | N | EntSpd | ExitSpd | PkBrk | LatG | Coast | Best exit` — the lap-to-lap spread of each segment phase across `crossLapComparableLaps`, followed by a "Most variable exit speed" ranking of the worst three. Speeds show mean ± SD; brake/LatG/coast show SD only, since their means are already in the phase table and the spread is the new information.

The header names the contributing lap numbers (`Consistency (2 laps: 3, 4)`) because that population is not the same as the lap list printed above it.

Pairs are matched by segment name + phase kind. A corner can split entry/mid/exit on one lap and collapse to a single `full` phase on another, so rows seen on fewer than 2 laps are dropped rather than shown with a misleading `± 0.0`.

### Fuel and stint planning
`Used | Remaining | Burn rate` — consumption from the `FuelLevel` channel, plus what it implies for stint length. `-fuel-laps N` adds a line answering whether the tank covers N more laps.

Consumption is the **difference between tank readings at each lap's boundaries**, not an integration of `FuelUsePerHour`. The rate channel is an instantaneous reading and integrating it accumulates error across a lap; the level difference is what actually went into the engine. `FuelUsePerHour` is still reported as an average, but nothing is derived from it.

Two figures are always given, average and worst-lap: planning a stint on the average runs dry half the time, so the worst-lap rate is the one a stint has to survive. `fuelMarginLaps` (1 lap) is expressed in laps rather than a percentage because that is how the decision is made in the pits — "a lap in hand" is a concrete amount of running, "5%" is not.

**Session endpoints are not used for totals.** Sessions routinely end with a pit stop that refills the tank, so `StartLitres - LastSample` reports a completed stint as having used nothing and having a full tank (observed on a real Phillip Island session: lap 5 pitted and refuelled to 49 L). `UsedLitres` is therefore the sum of per-lap consumption, and `EndLitres` is measured at the end of the last lap that did **not** refuel. A lap that gained fuel is flagged `Refuelled`, has its consumption zeroed (burn and fill can't be separated within one lap), and is excluded from the averages — and the fact is disclosed in the output rather than silently changing what the numbers mean.

Averages are taken over `crossLapComparableLaps` only. An out lap covers less than a full lap of track and would understate consumption; it still appears in the per-lap breakdown.

A flat or absent `FuelLevel` channel yields `Available: false` and the table is omitted entirely — reporting a session that burned no fuel would be worse than reporting nothing.

### Voice notes in analyze
`Lap | Where | Lap% | Note` — voice notes recorded during the session, placed on the lap and segment being driven when they were spoken.

The notes subcommand names each session file after the `.ibt` it detected, so the join is by filename (`<notesDir>/<ibt basename>.json`). A missing notes file is the normal case, not an error; an unparseable one warns on stderr and is skipped so it can't take down the telemetry analysis.

`notes.Note` carries **two** timestamps: `StartedAt` (recording start) and `Timestamp` (recording stop). Placement uses `Note.Anchor()`, which prefers `StartedAt` — the stop time trails the event by the whole length of the utterance, which is several hundred metres at racing speed. `StartedAt` is `omitempty`, so pre-existing notes files still load and fall back to `Timestamp`.

`-note-lag` (default 2s, `analysis.DefaultNoteLag`) is subtracted before placement to account for recognise-decide-press reaction time. It is an estimate rather than a measurement, which is why it is a flag; but zero would be a *worse* default, since a note about a corner can never be recorded before that corner happened.

Notes falling outside the recording window (spoken in the pits, or after the recording stopped) are printed with dashes for the position columns and counted in a footer, rather than dropped — the text is the point of the note.

The full stdout output is also copied to the system clipboard automatically (via `clip.exe` on Windows, `pbcopy` on macOS) — `(copied to clipboard)` is printed to stderr on success. Stdout is teed via an `os.Pipe` swap in `cmd/motorhome/main.go` around the `RunAnalyze` call (helpers in `cmd/motorhome/clipboard.go`); error paths that exit through `analyzeDie` (`os.Exit(1)`) skip the deferred clipboard write by design — partial broken output is intentionally not copied.

`-update-map` forces re-detection. `-geo-method latlon|lataccel` selects detection method. `-dump <segment>` writes a downsampled (20Hz) CSV of the segment's telemetry for AI analysis — accepts segment name (T3) or 1-based index (3). Output includes 1s of context before/after. The CSV is written **next to the `.ibt` being analysed**, not to the current working directory — a launcher (Stream Deck) gives no useful CWD and it may not be writable.

`-dump-all` (requires `-dump`) writes the same segment from every lap in `crossLapComparableLaps` into one `<seg>_alllaps.csv`, with a leading `Lap` column. Each lap's `Time` restarts at 0 so the traces can be overlaid at equal time-into-the-corner. The filename differs from the single-lap dump deliberately: the two have different column sets and must not overwrite each other. Laps with no samples in the segment are skipped (a truncated lap legitimately misses one); an error is raised only when no lap yielded rows.

### Sample-level traces (`-trace`, `-hz`)
`-trace T3,T4` prints the same rows `-dump` would write, **inline** rather than to a file, and puts them in the `-json` document's `traces` field. That inline form is what makes `coach -segment` possible: aggregate rows say an exit is slow and varies, but only the samples say the throttle came in 0.4s later on the lap that lost the time. Both paths share `DumpConfig` and `writeDumpRows`, so the numbers and their formatting are identical by construction (a test asserts the rows match `DumpSegmentAllLapsCSV` byte for byte).

Traces cover **every comparable lap**, not just the analysed one — comparing laps against each other is the point of zooming in. A session with no comparable set falls back to the analysed lap alone. Segments resolve through `analysis.ResolveSegmentList`, which accepts names or 1-based indices, dedupes, and returns them in track order regardless of the order given; an unrecognised entry fails the whole list rather than being skipped, because a typo that silently traced fewer corners would read as "that corner had no data".

`-hz N` sets the output rate for `-dump` and `-trace` (defaults: 20Hz for dump, 60Hz for trace) and is rejected without one of them. Rates that don't divide 60 evenly round to the nearest stride, so `DumpConfig.OutputHz` reports what was actually emitted rather than what was asked for.

**ABS and Coast are maxima over the window each row covers, not point samples.** Decimation suits continuous channels — speed read every third sample is still speed — but these two are 0/1 events, and taking every third one discarded two thirds of every ABS activation, so a lock-up lasting a few frames could vanish from a 20Hz dump entirely. That is the exact signal someone dumping a problem corner is looking for. This changed the existing `-dump` output too, and both dump headers now disclose it below 60Hz. Above 2000 trace rows a stderr note reports the size and suggests `-hz 20`, which — now that the binary channels survive downsampling — costs resolution on the continuous traces and nothing else.

The map line branches on whether a stored map was used, not on whether a match score was computed. A stored map with no comparable lap (no valid flying lap, or no track length in the YAML) prints its real geometry confidence, lap/session counts and `GeoMethod` with `match: n/a (no comparable lap)`. Keying this off the match score previously made those sessions report a mature map as a fresh `geometry: low` "first detection".

`-lap` accepts a positive integer (specific lap), empty (best lap of session — default), or `pb` (render the PB stored in `pb.json` without running the full analysis pipeline). For `-lap pb`: when an `.ibt` is available the car/track come from its session YAML; with no `.ibt` (or empty `ibtDir`) the single PB entry is used, or all entries are listed if there are several. The PB record stores `LapTime` / `Date` / `Weather`, the per-segment `Phases`, and the raw `CarSetup:` YAML block — enough to reproduce the setup and phase tables offline, without sample-level telemetry.

### coach subcommand flow (`cmd/motorhome/coach.go`)
Emits one self-contained markdown brief for an AI assistant: orientation → embedded `coach.md` framework → the analysis as JSON.

It exists so coaching is one command rather than a procedure. The assistant previously had to run `analyze`, separately read `coach.md`, and reconcile an ASCII table against a framework written for a human. Bundling the three removes the dependency on the assistant's working directory and on it remembering the extra steps.

**No network call and no API key** — the assistant reading the brief is the coach. This was a deliberate choice over calling the Anthropic API: it keeps the repo's zero-external-dependency property (see `internal/camera` and `internal/launcher`) and costs nothing per run.

`RunCoach` **reuses the analyze pipeline wholesale** by calling `RunAnalyze` in-process with `-json` and capturing stdout via `captureStdoutSilent`. A second analysis path would be free to drift from what `analyze` reports; the brief must describe exactly the same session.

`captureStdoutSilent` is the non-teeing sibling of `captureStdout`. The clipboard capture deliberately tees to the terminal; coach must not, or the raw JSON prints above the brief.

Because that pipeline runs in-process, its failures are *coach's* failures as far as the user is concerned. `invokedAs` (`analyze_helpers.go`) carries the subcommand name and the trace flag name, and `RunCoach` sets it before calling in — otherwise `coach -lap 99` reports `analyze: lap 99 not found`, and `coach -segment T99` blames `-trace`, sending the user after a flag that isn't on the command they ran. `dieMessage` is split out of `analyzeDie`/`coachDie` so the wording is assertable in-process; the wiring itself is covered by re-exec cases in `pb_exit_test.go`.

`trimForCoaching` drops the track map's raw segment geometry (entry/exit percentages and metre offsets), which carries no coaching signal — every segment is already named in the phase and consistency rows. `SegmentCount` stands in for it. Map confidence and match score are **kept**, because a low-confidence map means the segment boundaries themselves are suspect and findings pinned to them should be hedged. Nothing else is trimmed: phases and consistency dominate the payload but are the substance of the analysis.

The orientation ends with a `Gaps:` line naming what is absent (no track map, no PB, too few laps for consistency, no sector times). Coaching around a missing input without realising it is missing produces confident findings about nothing.

A missing `coach.md` is a stderr warning, not a fatal error — the data section still carries everything measured. `-no-framework` omits it deliberately.

**`-segment T3`** (`coach_focus.go`) narrows the brief to named corners and inlines their samples. The aggregate rows answer *which* corner is costing time; they cannot answer *what is happening in it*, because a mean and an SD describe a corner rather than replaying it. `focusOnSegments` filters only the per-segment collections — phases, vs-PB, exit impact, consistency. Session-level content stays whole (lap list, sectors, fuel, PB header, voice notes): it is small, it is the context that makes one corner interpretable, and a sector time is the one thing that says whether the focused corner is where the time is actually going. Segment geometry is kept because `-table` needs it to tell a corner from a straight.

The narrowing **must be disclosed or it is a trap**: a filtered brief is otherwise indistinguishable from a whole-session one, so `Focus` is set on the document and the orientation carries a bolded line saying other corners were removed and the lap must not be characterised as a whole. Without it the assistant reports the session's main problem having been shown one corner of it — the failure mode the `Gaps:` line already exists to prevent.

Traces are rendered as fenced CSV blocks **after** the JSON rather than inside it: they are already CSV, and nesting them in an indent-encoded object costs tokens and readability for nothing. `-hz N` sets their rate (default 60). `-table` accepts `-segment` but never the traces — a 60Hz CSV is not something a human scans between runs.

**`-table`** (`coach_table.go`) swaps the brief for a human-facing turn-by-turn view: one row per corner (`Turn | Speed in>min>out | Coast | Lock | Spin | ExitSD | Flags | Grade`), then a sector-loss table and a ranking of the least repeatable exits. Same `analyzeResult`, second renderer — the same relationship `analyze_json.go` has to the ASCII tables.

The **Grade is a triage hint, not a measurement**: the tool has no model of a well-driven corner, so the letter only counts how many of five fixed thresholds a corner trips (0 flags = A, 4+ = F). The thresholds are printed in the legend beneath the table so a grade can be argued with instead of trusted. There is deliberately **no "Fix" column** — deciding what to do about a flagged corner is the coaching judgement the assistant supplies, and generating it from thresholds would be a canned string dressed up as advice.

Straights are dropped from the table (a straight is either flat or a boundary artifact), which means braking that begins before the corner is not charged to it — the legend says so. `ExitSD` renders `-` below two comparable laps and cannot trip the spread flag: a one-lap corner has no spread, and grading it clean would be a lie of omission. Per-corner aggregation sums coast/lock/spin/corrections but takes the **worst** phase's `ExitSD`, since averaging one badly repeated phase against two steady ones hides the finding.

`-table` renders **before** `trimForCoaching`, because telling a corner from a straight needs the track map's `Kind` — exactly the geometry the brief drops. `cornerSegmentNames` falls back to the phase name (`full` = straight) when geometry is unavailable; that fallback misclassifies corners taken under 5° of steering, so it is a fallback rather than the rule.

### pb subcommand flow (`cmd/motorhome/pb.go`)
Reads back and maintains `pb.json`, which the analyze flow only ever appends to.

| Command | Behaviour |
|---|---|
| `pb list [filter]` | Table of every stored PB: car, track, time, date, and which optional payloads it carries (phases / setup / brake points) |
| `pb show <filter>` | Full record — setup tables and phase table — for exactly one matching entry |
| `pb diff [file.ibt]` | Setup differences between a session and that car/track's stored PB setup |
| `pb prune -older-than N \| -match F [-apply]` | Remove entries; previews unless `-apply` |

`<filter>` is a case-insensitive substring matched against `"car | track"`. `show` and `prune` refuse to guess — a filter matching zero or several entries lists the candidates and exits non-zero. Entries are sorted by track then car, because Go map order is random and listings would otherwise shuffle between runs.

**Prune previews by default.** Deleting a PB throws away accumulated brake-entry positions and the setup that produced the lap, none of which can be recovered from telemetry that may itself be long deleted. It also refuses to run with no criteria rather than selecting everything, and reports (rather than silently skipping) entries whose date won't parse.

**`pb diff` hides session state by default.** iRacing writes end-of-session tyre readings back into the `CarSetup` block (`LastHotPressure`, `LastTemps*`, `TreadRemaining`, `UpdateCount`), so a raw diff of two *identical* setups still reports every corner. `analysis.FilterSessionState` drops them and returns the count; `-all` shows them. On a real Mugello comparison this cut 29 rows to 16 genuine changes (ARB, wing angle, spring perch, dampers).

Note that iRacing's own YAML nests general chassis keys under the *last-seen* subsection, so paths like `Chassis/FrontBrakes/ArbSetting` and `Chassis/FrontBrakes/FuelLevel` are faithful to the source file, not a parser artifact.

### notes subcommand flow (`cmd/motorhome/notes.go`)
Toggle model — each press starts or stops recording:
1. First press → play start chime (A5→C6 via `kernel32.Beep`), start `audio.Recorder`
2. Second press → stop `Recorder`, play stop chime (E5→A4), transcribe via `whisper-cli`
3. Append `Note{Timestamp, Text}` to session JSON file; print `[note] transcribed text`

`notes set-hotkey` installs a keyboard hook and Raw Input listener simultaneously; first input wins and is saved to config. HID button-release events are discarded (toggle only cares about press).

### live subcommand flow (`cmd/motorhome/live.go`)
Reads an iRacing shared-memory snapshot via `iracing.ReadLiveData()` and prints your position, lap, and gap in seconds to the car directly ahead/behind on track. Default mode prints one frame and exits. `-watch` polls at `-hz` Hz (default 5, clamped 1–60) and prints one summary line per tick until Ctrl-C. `-raw` dumps every field of `LiveData` plus per-car detail for each valid CarIdx — use this when the formatted view looks wrong. Gap computation lives in `internal/iracing/gap.go` (`ComputeGaps`); driver-name lookup uses the `Drivers` map parsed from the session YAML. Solo practice sessions with no other cars show `Ahead/Behind: (none)` by design. Windows-only (`//go:build windows`).

### camera subcommand flow (`cmd/motorhome/camera.go`)
Restarts a stuck/frozen webcam by stopping (if running) and restarting the Windows `FrameServer`/`FrameServerMonitor` services — the shared pipeline every app uses to access a camera — rather than disabling/enabling the USB PnP device itself. This was a deliberate fallback: `Disable-PnpDevice`/`Enable-PnpDevice` and `pnputil` both require a genuine administrator token, which `motorhome.exe` does not have in normal (Stream Deck-launched) use, and `runas` elevation doesn't work in this environment (see Deployment below). Restarting the two named services only needs `SERVICE_START`/`SERVICE_STOP` rights on those specific services, which — like `SeDebugPrivilege` for `Kill()` — can be granted to the account directly via a one-time `sc sdset` ACL change (see [internal/camera/README.md](internal/camera/README.md)) instead of requiring full admin membership. Implementation is raw `advapi32.dll` Service Control Manager calls (`OpenSCManagerW`/`OpenServiceW`/`ControlService`/`StartServiceW`), matching the no-external-dependency style of `internal/launcher`. Windows-only (`//go:build windows`).

**Motivating case:** the webcam is redirected into a Remote Desktop session (`mstsc.exe`), and on leaving the meeting RDP never releases it — the device stays held indefinitely and every local app finds it busy. Nothing frees it short of a reboot or unplugging the camera. Restarting the Frame Server tears down that stale handle; verified to release the device with `mstsc.exe` holding it.

**There are two ends to this, and the command only fixes the one it runs on.** The camera is physically attached to the RDP *client*; the *host* runs its own `FrameServer` mediating the redirected device. A camera that reports "in use or unavailable" **inside** the session is stuck on the host, and running `camera` on the client will not clear it — the binary has to be run on the remote machine. Confirmed in practice: running `motorhome camera` on the remote machine cleared it with no reboot.

**Reconnecting the RDP session does not fix the host-side stall** (also confirmed). `FrameServer` is machine-wide rather than per-session, so tearing down and rebuilding the session leaves the stale handle untouched; only stopping the service releases it. Don't offer session reconnect as a workaround for this failure.

`camera` is therefore dispatched in `main.go` **before** `config.Load`, unlike every other subcommand. It reads nothing from the config, and the far end of an RDP session has no `launcher.config.json`; requiring one would fail a bare copy of the exe with a misleading "error loading config". `RunCamera` takes no `config.Config` parameter to keep that explicit.

A service that is already stopped is left alone rather than started: both are `DEMAND_START`, so Windows launches them on next camera access, and there's no stuck pipeline state to clear when nothing is running. The command restores the original service state instead of unconditionally leaving both running.

Runtime depends on whether the camera is held: ~0.01s when the services are already stopped, ~0.48s when running but idle, and 1–31s when a client holds the device — Windows waits for a graceful release that (with RDP) never comes, then forces it. `stopTimeout` is therefore 90s; it was 10s initially, which made a merely-slow restart report a *spurious failure*. After 2s a progress line explains what it's waiting on so a legitimate 30s wait doesn't look like a hang. The status-poll interval is 15ms — it was 200ms initially, which made the four waits round up to ~0.5s of dead time (most of the idle-case runtime) since stop/start each take only 20–110ms when uncontended.

Killing the hosting `svchost.exe` would be faster and is safely scoped (each service is alone in its svchost group), but both run as `LocalService`/`LocalSystem`, so terminating them non-elevated is expected to fail the same way `pnputil` did. The graceful stop works, so it isn't attempted.

Trade-off: restarts the camera pipeline system-wide (affects any camera in use, not just one device) and won't fix a true USB-level hardware hang — only a full PnP disable/enable or physical unplug/replug can, and that needs admin rights this tool doesn't have.

### Corner labelling and the Turns line
Detected corners are auto-labelled `T1`, `T2`, … in track order from the S/F line. **These are positional, not iRacing's official turn numbers.** Detection merges complexes, so the counts often differ — Road America reports `TrackNumTurns: 14` while detection finds 11 corner segments, and from the first merge onward every generated label is offset (the detected 8th corner is really the Kink).

The analyze output therefore prints a `Turns:` line comparing the two, e.g. `11 corners detected; iRacing reports 14 turns — labels are positional, not official`. A mismatch is not itself an error; it means the T-numbers can't be trusted as official ones.

To fix attribution, add `cornerNames` to the track's `trackref.json` entry: one free-text entry per **detected** corner, in track order. Entries can carry the number, a name, or both (`"T11 Kink"`); an empty entry keeps the generated label, so a track can be annotated a few corners at a time. Names are applied in memory by `trackmap.ApplyCornerNames` on every run and flow through the phase, vs-PB, exit-impact, `-dump` and `-trace`/`-segment` output — so a renamed corner is also what you pass to `coach -segment`.

`cornerNames` lives in `trackref.json`, not `trackmap.json`, because the latter is regenerated by detection and hand edits to it are lost.

Application is **all-or-nothing on length**: if the list doesn't have exactly one entry per detected corner, nothing is renamed and a warning is printed. A short or stale list would otherwise shift every subsequent label — the exact silent mislabelling the feature exists to prevent. The count is `trackmap.CountCorners` (corners + chicanes).

`analysis.ParseTrackNumTurns` reads the count from the session YAML. It is reported only, never used to drive detection: it counts iRacing's turn labels, not merged corner segments, so feeding it to `searchThresholds` as a target would push detection to over-split corners that are currently correct.

### Sector table
`Lap | S1..Sn | Lap` — per-sector times for every flying lap, with a `best` row and a theoretical-best line naming which lap each best sector came from. The fastest time in each sector is marked `*`.

Boundaries come from iRacing's own `SplitTimeInfo:` block in the session YAML (`SectorStartPct` per `SectorNum`), **not** from the detected track map — so they agree with the sim's own timing and are available even when no track map exists. Road America publishes 6 sectors; the count varies per track. Omitted entirely when the block is absent (some session types don't publish it).

Crossing times are linearly interpolated between samples (see `internal/analysis/sectors.go`); snapping to the nearest 60 Hz sample would cost up to 17 ms per boundary. Consequently the sectors sum to a few ms off the official `LapLastLapTime`, so the lap column prints the official time rather than the sum — showing the sum would look like a bug next to the lap list.

### Phase table columns
`Name | Phase | Spd (entry→exit km/h) | OnBrk | PkBrk | Thr% | LatG | Wheel° | Corr | ABS | Lock | Spin | Coast`
— Phase = entry/mid/exit/full. Straights get one "full" phase. Corners are split into entry/mid/exit using 80% of peak |SteeringAngle| as the commitment threshold. Corners with peak steering < 5° get a single "full" phase. Spd = entry and exit speed in km/h. OnBrk = % of phase time with brake applied (>2%). PkBrk = peak brake pressure. Thr% = samples at full throttle > 95%. LatG = mean abs(LatAccel)/9.81. Wheel° = peak absolute steering wheel angle in the phase (degrees; steering wheel, not road wheel — divide by steering ratio for tyre angle). Corr = steering direction reversals above threshold within the phase. ABS = samples with ABS active. Lock = samples where any wheel speed < 95% of vehicle speed under braking. Spin = samples where any wheel speed > 105% of vehicle speed under power. Coast = seconds (CoastSamples / 60).

### vs PB delta table
`Name | Phase | dSpd | dBrk | dPkBr | dThr | dLatG | dCorr | dABS | dLck | dSpn | dCoast`
— Shown after the phase table when stored PB phases exist. Each value is `current − PB`. Positive speed = faster than PB. Positive brake/coast/error counts = more than PB (usually worse). Phases are matched by segment name + phase kind; unmatched phases (e.g. track map changed) are skipped. Stored in `pb.json` as `phases` array inside `PersonalBest`.

When the current best lap *is* a new PB, the comparison uses the **previous** PB's phases — captured in `analyze.go` before `pb.Update` clears them, so the table shows how much the new PB beat the old lap by rather than comparing the lap to itself. The very first PB for a car/track has no prior to compare against, so the vs-PB table is omitted in that case.

### Corner exit → straight peak table
`Corner | ExitSpd | Straight | PeakSpd` — printed after the phase/vs-PB tables via `analysis.ComputeExitImpact` + `printExitImpact` in `analyze.go`. Pairs each corner/chicane with the straight segment immediately following it (wrapping from the last segment to the first, since the final corner typically leads onto the S/F straight) and shows the corner's exit speed alongside the peak speed reached on that straight — the direct measure of whether a slow exit cost speed down the next straight. `Phase` gained a `PeakSpeedKPH` field (max speed sample in the phase) to support this; it isn't shown in the main phase table to avoid widening an already-dense row. Omitted entirely when there are no corner→straight pairs (e.g. no track map).

### Telemetry channels extracted
SampleData extracts ~60 channels from .ibt files: core timing/position (LapDistPct, SessionTime, Speed, Lat, Lon), driver inputs processed and raw (Throttle/ThrottleRaw, Brake/BrakeRaw, Clutch, Gear, SteeringAngle), engine (RPM), vehicle dynamics (LongAccel, LatAccel, YawRate), driver aids (ABSActive, ABSCutPct, BrakeBias, TCSetting, ABSSetting), wheel speeds (LF/RF/LR/RR), tyre surface temps (4×3 L/M/R), tyre wear (4×3 L/M/R), tyre pressures (4), brake line pressures (4), fuel (FuelLevel, FuelUsePerHour), and steering feedback (SteeringWheelTorque). Missing channels default to zero.

iRacing's carcass-temp channels (`*tempCL/CM/CR`) were tried first but found to freeze at a stale cold value for an entire session on some cars (observed on the Porsche 718 GT4) — updating only once, right at session end — which corrupted `ComputeTyreSummary`'s per-lap average (all four corners reporting an identical, unchanging value). Surface temp (`*tempL/M/R`) updates every sample with physically plausible, corner-differentiated values and is used instead.

### Lap timing
`LapLastLapTime` is the authoritative lap time and matches the iRacing UI / Garage61. iRacing publishes it 0.1–1s *after* the S/F crossing (at the crossing frame itself the channel still holds the previous lap's value), so `ExtractLaps` tracks LLT across samples and applies each new positive value to the most recently finalized lap. The final lap of a recording, invalidated laps (LLT=-1), and recordings with no LLT channel fall back to `SessionTime[last] − SessionTime[first]`.

### Out/in lap detection
Out lap: first sample speed < 5 m/s. In lap: last sample speed < 5 m/s. Shown in lap list; excluded from best-lap selection unless forced with `-lap N`.

### Cut lap detection
Flying laps are scanned for shortcut gaps in `LapDistPct`: the track is binned into 100 buckets and any contiguous run of 3+ empty bins (≈ 180 m of skipped track at Watkins Glen) marks the lap as cut. Gated on `max(LapDistPct) >= 0.95` so a truncated final lap (recording stopped mid-lap) doesn't false-trigger. Cut laps render as `[flying lap, cut]` in the lap list and are excluded from `bestAnalyzeLap`, `flyingLapsWithinTime` (which feeds trackmap detection and brake-entry blending), `plausibleLapMinTime`, and PB updates. `-lap N` still works to inspect a specific cut lap.

Why this matters: iRacing's track-limits enforcement is lenient — a driver who shortcuts across an inner-loop chicane can still get a positive `LapLastLapTime` instead of the `-1` we'd otherwise rely on, and that cut lap will look fastest because it skipped real distance.

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
| `trackref.json` | hand-edited | expected corner counts per track (guides detection) + `cornerNames` corner labels |
| `pb.json` | auto on first `analyze` | personal best per car/track |

## Deployment
- Binary + config live in `G:\RACING\SimAppLauncher\` (the repo root)
- Stream Deck triggers via the **Open** action pointing directly at `G:\RACING\SimAppLauncher\motorhome.exe` with arguments `start` or `stop` — no PowerShell wrapper needed. Config path resolves relative to the exe via `os.Executable()`.
- UAC is set to never-notify on this machine — elevation via `ShellExecuteExW runas` does not work in this environment; use `elevate: false` for all apps
- SimHub auto-elevates via its own manifest and resists `taskkill` — the `SeDebugPrivilege` fallback in `Kill()` handles this
- Confirmed empirically (2026-07-28) that the process running `motorhome.exe` normally does **not** hold a full administrator token: `Disable-PnpDevice`/`pnputil /disable-device` both fail with access-denied against a real device. Only specific, narrowly-grantable privileges/rights (like `SeDebugPrivilege`, or the `camera` subcommand's service ACL) work without elevation — don't assume a feature needing genuine admin rights will work without first checking

## Known limitations
- `Minimized` window style not implemented (requires `golang.org/x/sys/windows` for `StartupInfo`; currently treated as `Normal`)
- `stop` kills by image name — affects all instances of a process if multiple are running
- `camera` restarts the Frame Server system-wide (not scoped to one device) and cannot fix a true USB-level hardware hang — only a full PnP disable/enable or physical unplug/replug can, which requires admin rights not available in this deployment
- `camera` can block up to ~30s when an app is holding the device; this is Windows waiting on the stuck client, not the tool, and cannot be shortened without admin rights to kill the service host
- `processName` whitespace is not trimmed — accidental spaces will cause silent match failures
- Segment detection with `lataccel` method only uses lateral G — pure braking zones with no lateral load appear as straights (`latlon` default avoids this)
- S/F line wraparound: tiny corners (< 50 m) at the S/F line are auto-removed, but if the first and last segments are both straights they are not merged into one
- GPS quantisation in iRacing is systematic (same rounding each lap) so averaging more laps does not reduce noise in the `latlon` method — mitigated by bin-averaging, wide triplet spacing, and post-detection validation (steering/speed confirmation)
- Dynamic weather sessions do not populate `AirTemp` in the session YAML; PB weather shows track temp only in that case
- `analyze` never prunes `pb.json` — old car/track combos accumulate indefinitely until removed with `motorhome pb prune`
- Voice notes are placed by wall clock, so their accuracy is bounded by `-note-lag` being an estimate; a note about a corner can land in the adjacent segment
- `coach -segment` and `analyze -trace` need a track map to name corners against, so neither works on the first session at a new track (nothing to resolve `T3` to yet)
- A focused trace is large: one long corner complex across five comparable laps at 60Hz is ~3,900 rows, several times the whole unfocused brief. `-hz 20` cuts it to about a third and no longer loses ABS or lock-ups, but the default is not free
- `pb diff` compares by setup-field path, so it cannot tell a genuine change from a field iRacing renamed between builds — those show as one added plus one removed field

## Open improvements
- Exit codes: `RunStart`/`RunStop` currently always exit 0 even on partial failures
- CSV parsing in `IsRunning` and `parsePIDFromTasklist` is naive — works because PID is always field[1], but would break if Windows changes the column order
- Same-direction corner complexes (e.g. Maggotts/Becketts) are not merged; only direction-reversing chicanes are detected
- `latlon` geo-method could be improved by using `VelocityX`/`VelocityY` channels (world-frame velocity) to compute heading-change rate instead of GPS curvature — avoids GPS quantisation entirely and should give a cleaner curvature proxy than bin-averaged lat/lon positions
- AI coaching via `-coach` flag: send the segment table, lap list, PB delta, and lap time trend to the Anthropic API and print actionable coaching feedback. Input is ~700 tokens (the existing analyze output as-is). Requires `ANTHROPIC_API_KEY` env var. Use `claude-haiku` for cost (~$0.001 per call).
