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
    USBDevices   []USBDevice // empty = usbdev's built-in list
}

type USBDevice struct {
    Alias string  // command-line target, e.g. "pedals"
    Name  string  // human-readable product name
    VID   string  // hex string, e.g. "0x30B7"
    PID   string  // hex string, e.g. "0x1001"
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
- A USB device with no `alias` or `name`, an unparseable `vid`/`pid`, a
  duplicate alias, or two aliases sharing one vid/pid
- An alias that is `"all"` (the wildcard target), starts with a dash (a command
  line would read it as a flag), or carries padding whitespace (it would never
  match what the user typed)

### Why the USB IDs are strings

`vid`/`pid` are hex **strings** (`"0x30B7"`), not numbers. JSON has no hex
literal, so a number would mean writing `12471` for `VID_30B7` — unrecognisable
next to the Device Manager entry it was copied from. `usbdev.ParseHexID` accepts
`0x30B7`, `30B7`, `30b7` and `VID_30B7`, so a paste from any of those places
works.

Validation parses with that *same* function rather than a lookalike check. The
property that matters is that `Validate` never accepts a document
`KnownUSBDevices` would then reject: the GUI settings panel writes only what
`Validate` passes, and a config that saves but does not load is the one failure
that panel must not have.

A non-empty `usbDevices` **replaces** `usbdev.KnownDevices` rather than extending
it — see [internal/usbdev](../usbdev/README.md) for why.

## Key functions

```go
cfg, err := config.Load("/path/to/launcher.config.json")
// Load calls Validate() internally and returns the error if invalid
```

The config path defaults to `launcher.config.json` in the same directory as the binary, resolved via `os.Executable()`. Override with `-config <path>`.
