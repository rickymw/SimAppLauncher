# internal/config

Config file loading and validation for `launcher.config.json`.

## What it does

Loads the JSON config from disk, validates all fields, and exposes the `Config` and `App` structs to the rest of the codebase.

## Data structures

```go
type Config struct {
    Driver       string  // iRacing UserName for multi-class car matching
    IbtDir       string  // directory scanned for .ibt files by analyze
    Hotkey       string  // key name for voice notes (e.g. "F13", "ScrollLock")
    WhisperPath  string  // path to whisper-cli.exe
    WhisperModel string  // path to whisper .bin model file
    Apps         []App
}

type App struct {
    Name        string  // display name (required)
    Path        string  // full path to exe (required)
    Args        string  // space-separated CLI args (split with strings.Fields)
    WindowStyle string  // "Normal" or "Hidden"
    DelayMs     int     // ms to wait after launch (≥ 0)
    Elevate     bool    // launch via ShellExecuteExW runas
    ProcessName string  // tasklist/taskkill image name; falls back to Name
}
```

## Validation

`Config.Validate()` rejects:
- Any app with an empty `name` or `path`
- Negative `delayMs`
- Unrecognised `windowStyle` (valid: `""`, `"Normal"`, `"Hidden"`)

## Key functions

```go
cfg, err := config.Load("/path/to/launcher.config.json")
// Load calls Validate() internally and returns the error if invalid
```

The config path defaults to `launcher.config.json` in the same directory as the binary, resolved via `os.Executable()`. Override with `-config <path>`.
