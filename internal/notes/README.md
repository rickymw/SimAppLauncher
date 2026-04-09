# internal/notes

Persists voice notes captured during a sim racing session.

## What it does

Defines the `Note` and `Session` types and provides load/save/append helpers. Each session's notes are stored as a single indented JSON file named after the associated `.ibt` file (or a plain timestamp if none is found).

## How it works

`AppendNote` is the main entry point: it loads the existing session file (or creates a new one), appends the note, and writes it back. The session file path is determined by the caller (`cmd/motorhome/notes.go`) before transcription completes.

## Data structures

```go
type Note struct {
    Timestamp time.Time  // UTC moment of key-release
    Text      string     // Whisper transcription
}

type Session struct {
    IbtFile string     // basename of associated .ibt file; "" if none
    Start   time.Time  // UTC time file was created
    Notes   []Note
}
```

## Key functions

```go
s, err := notes.LoadSession(path)   // returns empty Session if file missing
err  = notes.SaveSession(path, s)
err  = notes.AppendNote(path, note) // load → append → save in one call
```
