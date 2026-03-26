# SimAppLauncher
Application Launcher Designed for Sim Racers

A lightweight Windows CLI tool that launches, monitors, and closes all your sim racing apps in one command. Designed for Stream Deck integration.

## Features

- **Sequential launch** with per-app configurable delays
- **Idempotent start** — skips apps already running, no duplicate instances
- **Status check** — shows running/stopped state and PID for each app
- **Elevated process support** — can kill processes that auto-elevate (e.g. SimHub) via `SeDebugPrivilege`
- **Stream Deck ready** — simple CLI commands, no interactive UI
- **Config validation** — catches bad paths, negative delays, and invalid values at load time

## Requirements

- Windows 10/11
- [Go 1.21+](https://go.dev/dl/) (to build from source)

## Build

```powershell
go build -o simapplauncher.exe ./cmd/simapplauncher
```

To build directly into your launcher folder:

```powershell
go build -o "G:\RACING\launcher\simapplauncher.exe" ./cmd/simapplauncher
```

## Usage

```
simapplauncher [-config <path>] <start|stop|status>
```

The binary looks for `launcher.config.json` in the working directory by default. Use `-config` to point to a different file.

```powershell
# Launch all configured apps
.\simapplauncher.exe start

# Launch using a specific config file
.\simapplauncher.exe -config "G:\RACING\other\launcher.config.json" start

# Check which apps are running
.\simapplauncher.exe status

# Close all configured apps
.\simapplauncher.exe stop
```

### Example output

```
> simapplauncher start
  [+] SimHubWPF            ... launched (pid 41512)
  [+] Trading Paints       ... launched (pid 43996)
  [=] iRacingUI            ... already running (pid 43876)
  [+] MarvinsAIRA          ... launched (pid 46676)
  [!] MyApp                ... FAILED: path not found

Done. 3/5 apps running.

> simapplauncher status
  SimHubWPF            RUNNING  41512
  Trading Paints       RUNNING  43996
  iRacingUI            RUNNING  43876
  MarvinsAIRA          RUNNING  46676
  MyApp                STOPPED  -

> simapplauncher stop
  [-] SimHubWPF            ... closed
  [-] Trading Paints       ... closed
```

## Configuration

Edit `launcher.config.json` in the same directory as the binary:

```json
{
  "apps": [
    {
      "name": "SimHubWPF",
      "path": "G:\\Program Files (x86)\\SimHub\\SimHubWPF.exe",
      "args": "",
      "windowStyle": "Normal",
      "delayMs": 1500,
      "elevate": false,
      "processName": "SimHubWPF"
    }
  ]
}
```

| Field | Description |
|-------|-------------|
| `name` | Display name shown in CLI output |
| `path` | Full path to the executable (required) |
| `args` | Command-line arguments as a space-separated string |
| `windowStyle` | `Normal` or `Hidden` (default: `Normal`) |
| `delayMs` | Milliseconds to wait after launching this app before the next (must be >= 0) |
| `elevate` | Launch via `ShellExecuteEx` with `runas` verb |
| `processName` | Exe stem used for status checks and stop (e.g. `SimHubWPF` for `SimHubWPF.exe`). Falls back to `name` if empty. |

> **Note:** `processName` must match the image name shown in Task Manager. Many apps spawn a child process with a different name — if `status` shows an app as STOPPED immediately after launching it, check Task Manager for the real process name.

## Stream Deck Integration

Use the Stream Deck **Open** action pointing directly at the binary — no PowerShell wrapper needed.

**Action:** `Open`
**App/File:** `G:\RACING\launcher\simapplauncher.exe`
**Arguments:** `start` (or `stop`)

The binary resolves `launcher.config.json` relative to its own location, so the working directory doesn't matter.

## Testing

**Unit tests** (fast, no real processes):
```powershell
go test ./...
```

**End-to-end test** (launches and closes your actual apps, ~20s):
```powershell
go test -tags e2e -v ./internal/launcher/ -run TestE2E_FullStack -timeout 120s
```

## Known Limitations

- `Minimized` window style is not implemented — it falls back to `Normal`. Full support requires `golang.org/x/sys/windows`.
- `stop` kills all instances of a process by image name. If you have multiple instances of the same exe running, all will be closed.

## Project Structure

```
cmd/simapplauncher/
  main.go                     # CLI entry point — flag parsing and subcommand dispatch

internal/
  config/
    config.go                 # Config struct definitions and validation
    load.go                   # JSON config loader
    load_test.go              # Config loading and validation tests
  launcher/
    launcher.go               # RunStart / RunStop / RunStatus + ProcessManager interface
    process_windows.go        # Windows implementation: Spawn, IsRunning, Kill (with SeDebugPrivilege fallback)
    elevate_windows.go        # UAC elevation via ShellExecuteExW
    output.go                 # Formatted print helpers
    launcher_test.go          # Unit tests with mock ProcessManager
    output_test.go            # Output formatting tests
    e2e_windows_test.go       # Full stack e2e test against real apps

launcher.config.json          # App configuration
```
