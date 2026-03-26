# CLAUDE.md — SimAppLauncher

## Project overview
Windows CLI tool (`simapplauncher.exe`) that launches, monitors, and closes sim racing apps in sequence. Designed for Stream Deck integration. Three subcommands: `start`, `stop`, `status`. Accepts an optional `-config <path>` flag.

## Build
```powershell
go build -o simapplauncher.exe ./cmd/simapplauncher

# Deploy to launcher folder
go build -o "G:\RACING\launcher\simapplauncher.exe" ./cmd/simapplauncher
cp launcher.config.json "G:\RACING\launcher\launcher.config.json"
```

## Tests
```powershell
# Unit tests (always run these — 23 tests, no real processes)
go test ./...

# Full stack e2e (launches real apps — takes ~20s)
go test -tags e2e -v ./internal/launcher/ -run TestE2E_FullStack -timeout 120s
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

The `processName` field is the exe stem used for `tasklist`/`taskkill`. Falls back to `name` if empty. Must match Task Manager's image name, which may differ from the launched exe if the app spawns a child process.

### `Args` field
`config.App.Args` is a `string`, not `[]string`. Split with `strings.Fields(app.Args)` before passing to `exec.Command`.

## Deployment
- Binary + config deployed to `G:\RACING\launcher\`
- Stream Deck triggers via the **Open** action pointing directly at `G:\RACING\launcher\simapplauncher.exe` with arguments `start` or `stop` — no PowerShell wrapper needed. Config path resolves relative to the exe via `os.Executable()`.
- UAC is set to never-notify on this machine — elevation via `ShellExecuteExW runas` does not work in this environment; use `elevate: false` for all apps
- SimHub auto-elevates via its own manifest and resists `taskkill` — the `SeDebugPrivilege` fallback in `Kill()` handles this

## Known limitations
- `Minimized` window style not implemented (requires `golang.org/x/sys/windows` for `StartupInfo`; currently treated as `Normal`)
- `stop` kills by image name — affects all instances of a process if multiple are running
- `processName` whitespace is not trimmed — accidental spaces will cause silent match failures

## Open improvements
- Exit codes: `RunStart`/`RunStop` currently always exit 0 even on partial failures
- CSV parsing in `IsRunning` and `parsePIDFromTasklist` is naive — works because PID is always field[1], but would break if Windows changes the column order
