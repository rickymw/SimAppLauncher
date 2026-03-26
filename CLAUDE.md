# CLAUDE.md — SimAppLauncher

## Project overview
Windows CLI tool (`simapplauncher.exe`) that launches, monitors, and closes sim racing apps in sequence. Designed for Stream Deck integration. Three subcommands: `start`, `stop`, `status`.

## Build
```powershell
go build -o simapplauncher.exe ./cmd/simapplauncher

# Deploy to launcher folder
go build -o "G:\RACING\launcher\simapplauncher.exe" ./cmd/simapplauncher
cp launcher.config.json "G:\RACING\launcher\launcher.config.json"
```

## Tests
```powershell
# Unit tests (always run these)
go test ./...

# Full stack e2e (launches real apps — takes ~12s)
go test -tags e2e -v ./internal/launcher/ -run TestE2E_FullStack -timeout 60s
```

## Architecture

### Key interface
`ProcessManager` in `internal/launcher/launcher.go` abstracts all OS process operations. The Windows implementation is in `process_windows.go`. Tests use a `mockPM` struct with configurable function fields.

### Windows process management
- **Launch**: `os/exec` + `syscall.SysProcAttr{HideWindow: bool}`
- **Status**: `tasklist /FI "IMAGENAME eq name.exe" /NH /FO CSV`
- **Stop**: `taskkill /F /IM name.exe`
- **Elevation**: `ShellExecuteExW` via `shell32.dll` (used when `elevate: true` in config)

### Config
`launcher.config.json` lives next to the binary. The `processName` field is the exe stem used for tasklist/taskkill — must match Task Manager's image name, which may differ from the launched exe if the app spawns a child process.

### `Args` field
`config.App.Args` is a `string`, not `[]string`. Split with `strings.Fields(app.Args)` before passing to `exec.Command`.

## Deployment
- Binary + config deployed to `G:\RACING\launcher\`
- Stream Deck triggers via `pwsh -ExecutionPolicy Bypass -WorkingDirectory "G:\RACING\launcher" -Command ".\simapplauncher.exe start/stop"`
- UAC is set to never-notify on this machine — elevation via `ShellExecuteExW runas` does not work; use `elevate: false` for all apps

## Known limitations
- `Minimized` window style not implemented (requires `golang.org/x/sys/windows` for `StartupInfo`; currently treated as `Normal`)
- Apps that spawn child processes under a different name need their `processName` set to the child's image name, not the launcher exe name
- `stop` kills by image name — affects all instances of that process
