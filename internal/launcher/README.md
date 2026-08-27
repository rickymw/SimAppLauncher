# internal/launcher

Process management for sim racing apps: launch, status check, and kill.

## What it does

Implements the `start`, `stop`, and `status` subcommands. Iterates the app list from config, checks each process with `tasklist`, and spawns or kills as needed. Handles auto-elevating processes (e.g. SimHub) via a `SeDebugPrivilege` fallback.

## How it works

### Computed results vs printed lines

`Status`, `Start` and `Stop` (`ops.go`) do the work and return `[]AppResult`.
`RunStatus`, `RunStart` and `RunStop` (`launcher.go`) are thin printers over
them.

That split exists because the GUI needs these as data, not as formatted lines,
and a second implementation of "is this app running, and if not, launch it"
would be free to drift from what the CLI reports. It is the same relationship
`analyze_json.go` has to the ASCII tables: one set of values, two renderers.

`AppResult.Err` is a string rather than an `error` because these are marshalled
straight to JSON by the GUI, and an error value would encode as `{}`.
`AppResult.Running()` treats *launched*, *already-running* and *running* alike,
and deliberately excludes *failed* — counting a failed status check as running
would report the rig as up on the strength of an error.

The per-app `delayMs` sleep stays inside `Start` rather than being hoisted into
the caller. It is part of what "start the rig" means — some apps will not attach
until the one before them is up — so a caller that skipped it would be starting
a different sequence, not the same one faster.

### ProcessManager interface

All OS calls go through a `ProcessManager` interface so tests can inject a `mockPM` without touching real processes:

```go
type ProcessManager interface {
    Spawn(app config.App) SpawnResult
    IsRunning(processName string) (pid int, running bool, err error)
    Kill(processName string) error
}
```

### Windows implementation (`process_windows.go`)

- **`IsRunning`**: runs `tasklist /FI "IMAGENAME eq name.exe" /NH /FO CSV` and parses the PID from field [1] of the first CSV row.
- **`Spawn`**: uses `os/exec` + `syscall.SysProcAttr{HideWindow: bool}`. If `app.Elevate` is true, delegates to `elevate_windows.go`.
- **`Kill`**: first tries `taskkill /F /IM name.exe`. If that fails (typically because the process is elevated), acquires `SeDebugPrivilege` via `advapi32`, then calls `OpenProcess` + `TerminateProcess` via `kernel32`.

### Elevation (`elevate_windows.go`)

`ShellExecuteExW` with verb `"runas"` — triggers a UAC prompt. UAC is disabled on the deployment machine so `elevate: true` is unused in practice; the code is present for completeness.

### Shared Windows API declarations

`kernel32` is declared in `elevate_windows.go` and shared across all Windows files in the package. `advapi32` is declared in `process_windows.go`. Both are package-level vars — no redeclaration needed.

## Architecture

| File | Contents |
|---|---|
| `launcher.go` | `ProcessManager` interface; `RunStart`, `RunStop`, `RunStatus` — printers over `ops.go` |
| `ops.go` | `Status`, `Start`, `Stop` returning `[]AppResult`; `Outcome`, `CountRunning`, `processName` |
| `process_windows.go` | `Spawn`, `IsRunning`, `Kill`, `SeDebugPrivilege` fallback |
| `elevate_windows.go` | UAC elevation via `ShellExecuteExW` |
| `output.go` | Formatted print helpers (`PrintLaunched`, `PrintClosed`, etc.) |
