# CLAUDE.md — SimAppLauncher

## Project overview
Windows CLI tool (`simapplauncher.exe`) that launches, monitors, and closes sim racing apps in sequence, and analyses iRacing `.ibt` telemetry files. Designed for Stream Deck integration. Four subcommands: `start`, `stop`, `status`, `analyze`. Accepts an optional `-config <path>` flag.

## Build
```powershell
go build -o simapplauncher.exe ./cmd/simapplauncher
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
simapplauncher start                                   # launch all apps in config order
simapplauncher stop                                    # kill all apps
simapplauncher status                                  # print running/stopped state
simapplauncher analyze session.ibt                     # best flying lap from file
simapplauncher analyze -lap 3 session.ibt              # specific lap
simapplauncher analyze -compare 2,3 session.ibt        # side-by-side lap comparison
```

## Architecture

### Key interface
`ProcessManager` in `internal/launcher/launcher.go` abstracts all OS process operations. The Windows implementation is in `process_windows.go`. Tests use a `mockPM` struct with configurable function fields — `spawnFn`, `runningFn`, `killFn`.

### Windows process management
- **Launch**: `os/exec` + `syscall.SysProcAttr{HideWindow: bool}`
- **Status**: `tasklist /FI "IMAGENAME eq name.exe" /NH /FO CSV` — returns `(pid, running, error)`
- **Stop**: `taskkill /F /IM name.exe`, with fallback to `SeDebugPrivilege` + `OpenProcess`/`TerminateProcess` for elevated processes (e.g. SimHub)
- **Elevation**: `ShellExecuteExW` via `shell32.dll` (used when `elevate: true` in config)

### Shared Windows API declarations
`kernel32` is declared in `elevate_windows.go` and shared across all `//go:build windows` files in the `launcher` package. `advapi32` is declared in `process_windows.go`. Both are package-level vars — no redeclaration needed in other files.

### Config
`launcher.config.json` lives next to the binary by default. Override with `-config <path>`. Config is validated on load — rejects empty `name`/`path`, negative `delayMs`, and invalid `windowStyle`.

Top-level fields:
- `driver` — iRacing `UserName` (e.g. `"Ricky Maw"`). Used by `analyze` to match the player's car in multi-class sessions. Case-insensitive. Falls back to `DriverCarIdx` if omitted or no match.
- `apps` — list of apps to launch/stop.

The `processName` field is the exe stem used for `tasklist`/`taskkill`. Falls back to `name` if empty. Must match Task Manager's image name, which may differ from the launched exe if the app spawns a child process.

### `Args` field
`config.App.Args` is a `string`, not `[]string`. Split with `strings.Fields(app.Args)` before passing to `exec.Command`.

### Telemetry analysis (`internal/analysis`)
Parses iRacing `.ibt` binary files and produces per-lap zone statistics.

- **`internal/ibt`** — low-level `.ibt` parser: reads the file header, variable descriptors, and raw sample buffers. `File.Sample(i)` returns a typed accessor for all channels at sample `i`.
- **`internal/analysis/lap.go`** — `ExtractLaps` splits the sample stream into `Lap` objects at S/F crossings (detected by `LapDistPct` dropping > 0.5). Classifies each lap as `flying`, `out lap`, `in lap`, or `out/in lap` based on entry/exit speed. Only flying laps are used by default.
- **`internal/analysis/zones.go`** — `ZoneStats` divides a lap into 20 equal-distance zones (5% each) and computes speed, inputs, G-forces, ABS count, and coasting samples per zone. `ZoneDeltas` interpolates `SessionTime` at zone boundaries to produce a per-zone time delta between two laps.
- **`cmd/simapplauncher/analyze.go`** — `RunAnalyze` implements the `analyze` subcommand: opens the `.ibt` file, resolves session metadata, prints the lap list, then prints a zone table or comparison table.

#### Out/in lap detection
A lap is flagged as an **out lap** if the first sample's speed < 5 m/s (rolling from pit/grid). It is flagged as an **in lap** if the last sample's speed < 5 m/s (pulling into pit lane). Both together = **out/in lap**. Out/in laps are shown in the lap list but excluded from best-lap selection and not used as comparison targets unless explicitly requested with `-lap N`.

#### Driver/car resolution
`ParseSessionMeta(yaml, driverName)` in `lap.go`:
1. Scans the `Drivers:` list for a block whose `UserName` matches `driverName` (case-insensitive).
2. Falls back to `DriverCarIdx` (the recording driver's own index).
3. Last resort: first `CarScreenName` in the file.

This ensures the correct car is shown in multi-class sessions where many different car types are listed.

## Deployment
- Binary + config live in `G:\RACING\SimAppLauncher\` (the repo root)
- Stream Deck triggers via the **Open** action pointing directly at `G:\RACING\SimAppLauncher\simapplauncher.exe` with arguments `start` or `stop` — no PowerShell wrapper needed. Config path resolves relative to the exe via `os.Executable()`.
- UAC is set to never-notify on this machine — elevation via `ShellExecuteExW runas` does not work in this environment; use `elevate: false` for all apps
- SimHub auto-elevates via its own manifest and resists `taskkill` — the `SeDebugPrivilege` fallback in `Kill()` handles this

## Known limitations
- `Minimized` window style not implemented (requires `golang.org/x/sys/windows` for `StartupInfo`; currently treated as `Normal`)
- `stop` kills by image name — affects all instances of a process if multiple are running
- `processName` whitespace is not trimmed — accidental spaces will cause silent match failures
- `ZoneDeltas` requires both laps to have monotonically increasing `LapDistPct`; laps with backward tracking (e.g. short-cuts) may produce incorrect deltas

## Open improvements
- Exit codes: `RunStart`/`RunStop` currently always exit 0 even on partial failures
- CSV parsing in `IsRunning` and `parsePIDFromTasklist` is naive — works because PID is always field[1], but would break if Windows changes the column order
