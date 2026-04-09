# internal/trackmap

Geometry-based corner/straight detection and persistent track segment storage.

## What it does

- Detects corners, straights, and chicanes automatically from telemetry
- Persists results to `trackmap.json` keyed by track name
- Tracks geometry confidence (low/moderate/high) based on laps seen
- Scores how well a new session's telemetry matches stored boundaries

## How it works

### Files

| File | Contents |
|---|---|
| `detect.go` | Constants, types (`Sample`, `rawSeg`), detection entry points (`Detect`, `DetectFromMultiple`, `DetectFromMultipleLatLon`, `searchThresholds`, `countCornerSegs`, `detectRawFromProfiles`, `detectFromProfiles`) |
| `profiles.go` | Signal processing: `buildProfile`, `buildSpeedProfile`, `buildSteerProfile`, `buildPositionProfile`, `project`, `signedCurvature` |
| `postprocess.go` | Post-detection validation pipeline: `trimWraparoundCorner`, `confirmCorners`, `validateCornerSpeed`, `refineBoundaries`, `splitLargeCorners`, `findTroughs`, `findSplitPoints` |
| `util.go` | Bucket math, merge helpers, `MatchScore`, `labelSegments`: `allZero`, `bucketMean`, `bucketMinMax`, `fillGaps`, `boxSmooth`, `hysteresis`, `groupBuckets`, `avgSign`, `mergeShort`, `mergeIdx`, `mergeAt`, `mergeChicanes` |
| `trackmap.go` | `TrackMap`, `TrackMapFile`, `Segment`, `Load`, `Save`, confidence logic |
| `trackref.go` | `TrackRef`, `TrackRefFile`, `LoadTrackRef` |

### Detection pipeline

Both methods share the same final pipeline (`detectFromProfiles`):

1. **Bucket** samples into 1000 position bins (0.1% resolution)
2. **Forward-fill** empty bins from neighbours
3. **Box-smooth** with a window proportional to ~15 m of track
4. **Hysteresis classify**: enter corner at ≥ threshold, exit at < lower threshold
5. **Group** consecutive same-class buckets into raw segments
6. **Merge short** segments repeatedly until stable (min 1.2% straight, 0.6% corner)
7. **Merge chicanes**: `[corner, short-straight ≤ ~100 m, opposite-direction corner]` → single chicane
8. **Label**: S1/S2… for straights, T1/T2… for corners, T3-T4 for chicanes

**`latlon` method** (`DetectFromMultipleLatLon`): projects GPS lat/lon to local XY metres using equirectangular projection, accumulates bin-averaged positions across all laps, then computes signed curvature using triplets spaced ~20 m apart. Thresholds: κ ≥ 0.004 m⁻¹ (enter), < 0.0015 m⁻¹ (exit). Returns `nil` if lat/lon channels absent — caller falls back to lataccel.

After initial detection, the latlon path applies four post-processing validation steps:

1. **S/F wraparound trim** (`trimWraparoundCorner`): removes tiny GPS-artifact corners (< 50 m) at the start/finish line
2. **Steering/lateral-G confirmation** (`confirmCorners`): reclassifies corners as straights if they have neither meaningful steering (< 10° mean) nor lateral load (< 2.0 m/s²)
3. **Oversized corner splitting** (`splitLargeCorners`): splits corners > 200 m that contain multiple speed troughs separated by re-acceleration > 20 km/h
4. **Speed-profile validation** (`validateCornerSpeed`): reclassifies corners with flat speed (< 10 km/h variation) as straights

Chicane merging also enforces a maximum total length of 400 m to prevent merging genuinely separate corners (e.g. Redgate + Hollywood at Donington).

5. **Boundary refinement** (`refineBoundaries`): adjusts each straight↔corner boundary using steering and lateral-G profiles. If the straight side of a boundary has active cornering (steering ≥ 7° or lat-G ≥ 2.5), the boundary shifts into the straight. If the corner side has no cornering activity, the boundary shifts into the corner. Short straights that get absorbed are merged into adjacent corners.

### Target-guided detection (`trackref.go`)

When `trackref.json` exists alongside the binary, the latlon detector reads the expected corner count for the current track and searches across curvature threshold candidates to find the one that produces the correct number of corner segments. This eliminates the need to manually tune thresholds per track. Tracks not in the reference fall back to default thresholds.

**`lataccel` method** (`DetectFromMultiple`): averages `abs(LatAccel)` element-wise across laps. Thresholds: ≥ 5.0 m/s² (enter), < 2.5 m/s² (exit).

`MatchScore` re-runs the lataccel pipeline on the current session's best lap and checks whether each stored boundary has a transition within ±2%. Always uses lataccel for consistency. Returns 0.0–1.0.

### Persistent storage (`trackmap.go`)

`trackmap.json` is a flat JSON map keyed by `TrackDisplayName`. Each entry (`TrackMap`) stores:
- `segments` — detected segment list
- `lapsUsed` / `sessionsUsed` — cumulative confidence counters
- `seenSessions` — RFC3339 session start dates (deduplication, capped at 50)
- `geoMethod` — `"latlon"` or `"lataccel"`

Each `Segment` stores geometric boundaries (`entryPct`/`exitPct`) and an optional `brakeEntryPct` — the average distance where drivers begin braking, blended with weighted averaging across sessions.

## Architecture

| Symbol | Description |
|---|---|
| `Segment` | One straight/corner/chicane: name, kind, entry/exit pct+metres, brakeEntryPct. |
| `TrackMap` | Full segment map for one track with confidence metadata. |
| `TrackMapFile` | Top-level JSON type: `map[string]*TrackMap`. |
| `GeometryConfidence` | `"low"` / `"moderate"` / `"high"` based on laps used. |
| `SegmentKind` | `"straight"`, `"corner"`, `"chicane"`. |
| `TrackRef` | Expected corner count for a known track. |
| `TrackRefFile` | Top-level JSON type for `trackref.json`: `map[string]*TrackRef`. |

### Key functions

```go
tmf, err := trackmap.Load("trackmap.json")
tm := tmf["Donington Park"]

segs := trackmap.DetectFromMultipleLatLon(allLapSamples, trackLengthM)
// falls back to:
segs = trackmap.DetectFromMultiple(allLapSamples, trackLengthM)

score := trackmap.MatchScore(bestLapSamples, segs, trackLengthM)
conf := tm.EffectiveConfidence(score)

trackmap.Save("trackmap.json", tmf)
```
