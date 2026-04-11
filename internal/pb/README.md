# internal/pb

Personal best lap time tracking per car/track combination.

## What it does

Loads, updates, and saves a flat JSON store of the fastest recorded lap for each car+track pair. Called by `analyze` after every session.

## How it works

The store is a `map[string]*PersonalBest` keyed by `"Car|Track"`. On each `analyze` run the best flying lap is compared against the stored PB; if it's faster (or no PB exists yet), the entry is updated and the file is saved. `Update` returns `true` when a new PB is set so the caller can print a notification.

`Save` uses atomic write (write-to-temp-then-rename) to prevent file corruption if interrupted mid-write.

## Architecture

| Symbol | Description |
|---|---|
| `PersonalBest` | Lap time (seconds + formatted string), date, weather string, car, track. |
| `File` | `map[string]*PersonalBest` — the top-level JSON type. |

### Key functions

```go
pbf, err := pb.Load("pb.json")

isNew := pb.Update(pbf, car, track, lapTime, "2:11.367", "2026-03-31", "Air 22°C, Track 35°C")
if isNew {
    // print PB notification
}

err = pb.Save("pb.json", pbf)
```

`Key(car, track)` returns the map key; `Load` returns an empty `File` (not an error) when the file does not yet exist.
