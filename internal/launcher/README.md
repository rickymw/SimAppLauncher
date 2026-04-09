# internal/launcher

Process management for sim racing apps: launch, status check, and kill.

## What it does

Implements the `start`, `stop`, and `status` subcommands. Iterates the app list from config, checks each process with `tasklist`, and spawns or kills as needed. Handles auto-elevating processes (e.g. SimHub) via a `SeDebugPrivilege` fallback.

## How it works

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
| `launcher.go` | `ProcessManager` interface, `RunStart`, `RunStop`, `RunStatus` |
| `process_windows.go` | `Spawn`, `IsRunning`, `Kill`, `SeDebugPrivilege` fallback |
| `elevate_windows.go` | UAC elevation via `ShellExecuteExW` |
| `output.go` | Formatted print helpers (`PrintLaunched`, `PrintClosed`, etc.) |
