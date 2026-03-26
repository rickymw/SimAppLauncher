# SimAppLauncher
Application Launcher Designed for Sim Racers

A lightweight Windows CLI tool that launches, monitors, and closes all your sim racing apps in one command. Designed for Stream Deck integration.

## Features

- **Sequential launch** with per-app configurable delays
- **Idempotent start** — skips apps already running, no duplicate instances
- **Status check** — shows running/stopped state and PID for each app
- **Elevation support** — per-app UAC elevation via `ShellExecuteEx`
- **Stream Deck ready** — simple CLI commands, no interactive UI

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
simapplauncher <start|stop|status>
```

The binary looks for `launcher.config.json` in the working directory.

```powershell
# Launch all configured apps
.\simapplauncher.exe start

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

Done. 3/5 apps launched.

> simapplauncher status
  SimHubWPF            RUNNING  41512
  Trading Paints       RUNNING  43996
  iRacingUI            RUNNING  43876
  MarvinsAIRA          RUNNING  46676
  MyApp                STOPPED  -
```

## Configuration

Edit `launcher.config.json` in the same directory as the binary:

```json
{
  "logFile": ".\\launcher.log",
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
| `path` | Full path to the executable |
| `args` | Command-line arguments (space-separated string) |
| `windowStyle` | `Normal`, `Hidden` |
| `delayMs` | Milliseconds to wait after launching this app before the next |
| `elevate` | Launch via `ShellExecuteEx` with `runas` verb |
| `processName` | Executable name (without `.exe`) used for status checks and stop |

> **Note:** `processName` must match the image name shown in Task Manager, which may differ from the launched executable if the app spawns a child process.

## Stream Deck Integration

Use the Stream Deck **Open** action with these commands:

**Start:**
```
pwsh -ExecutionPolicy Bypass -WorkingDirectory "G:\RACING\launcher" -Command ".\simapplauncher.exe start"
```

**Stop:**
```
pwsh -ExecutionPolicy Bypass -WorkingDirectory "G:\RACING\launcher" -Command ".\simapplauncher.exe stop"
```

## Testing

**Unit tests** (fast, no real processes):
```powershell
go test ./...
```

**End-to-end test** (launches and closes your actual apps):
```powershell
go test -tags e2e -v ./internal/launcher/ -run TestE2E_FullStack -timeout 60s
```

## Project Structure

```
cmd/simapplauncher/
  main.go                     # CLI entry point — subcommand dispatch

internal/
  config/
    config.go                 # Config struct definitions
    load.go                   # JSON config loader
    load_test.go              # Config loading tests
  launcher/
    launcher.go               # RunStart / RunStop / RunStatus + ProcessManager interface
    process_windows.go        # Windows implementation: Spawn, IsRunning, Kill
    elevate_windows.go        # UAC elevation via ShellExecuteExW
    output.go                 # Formatted print helpers
    launcher_test.go          # Unit tests with mock ProcessManager
    output_test.go            # Output formatting tests
    e2e_windows_test.go       # Full stack e2e test against real apps

launcher.config.json          # App configuration
```
