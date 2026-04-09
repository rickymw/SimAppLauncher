# internal/analysis

Extracts per-lap statistics from iRacing `.ibt` telemetry samples.

## What it does

- Splits a raw sample stream into `Lap` objects at start/finish crossings
- Classifies each lap as flying, out lap, in lap, or out/in lap
- Computes per-phase statistics (speed, inputs, G-forces, steering metrics, ABS, coasting, TC intervention, lockup/wheelspin) by splitting corners into entry/mid/exit phases using steering angle
- Computes per-segment aggregate statistics (speed, inputs, G-forces, ABS, coasting, TC intervention, lockup/wheelspin, tyre temps) against geometry-based track segments
- Detects average braking-onset distance for each corner/chicane

## How it works

### Lap extraction (`lap.go`)

`ExtractLaps` scans all samples and splits at S/F crossings: any step where `LapDistPct` drops by more than 0.5. A single-sample artifact (iRacing briefly sets `LapDistPct=0` at the exact crossing frame) is absorbed rather than creating a spurious extra lap. Laps shorter than 300 samples (5 s at 60 Hz) are discarded.

**Lap timing:** `LapLastLapTime` is read from the S/F crossing frame (the artifact frame in the zero-artifact case, or the first frame of the new lap in the normal case). When present and > 0 it is stored as `OfficialLapTime` and used as `LapTime` — matching the time shown in iRacing and third-party tools like Garage61. If the channel is absent, `LapTime` falls back to `SessionTime[last] − SessionTime[first]`, which can differ by up to ~33 ms per boundary.

Out/in lap classification uses entry/exit speed: < 5 m/s at the first sample = out lap, < 5 m/s at the last sample = in lap.

### Segment statistics (`zones.go`)

`SegmentStats` iterates the lap's samples and buckets each one into a geometry segment using *effective boundaries*. For corners/chicanes that have a stored `BrakeEntryPct`, the effective entry is that value rather than the geometric `EntryPct`; the preceding straight's exit is clipped to match. This ensures braking-zone samples are attributed to the corner, not the straight.

`ComputeBrakeEntries` scans flying laps backward from each corner's geometric entry to find the average point where brake pressure first exceeds 5%. A tolerance of 3 consecutive non-braking samples prevents ABS modulation from terminating the scan early.

### Phase analysis (`phases.go`)

`ComputePhases` splits each segment into steering-based phases and computes per-phase statistics. Straights get one "full" phase. Corners are split into entry/mid/exit using the steering angle trace:

1. Find peak `|SteeringAngle|` across all samples in the segment
2. Entry: start → first sample reaching 80% of peak
3. Mid: samples at ≥ 80% of peak (committed to the arc)
4. Exit: last sample dropping below 80% of peak → end

Corners with peak steering < 5° get a single "full" phase. `countSteeringCorrections` detects rapid sign changes in steering rate within each phase.

### Legacy zone stats

`ZoneStats` divides the track into 20 equal 5% zones. Retained but not used by the CLI.

## Architecture

| Symbol | Description |
|---|---|
| `SampleData` | ~60 telemetry channels per sample: timing, driver inputs (raw & processed), dynamics, driver aids, wheel speeds, tyre temps/wear/pressure, brake line pressures, fuel, steering torque. |
| `Lap` | One lap: number, time (`LapLastLapTime` preferred; SessionTime diff fallback), kind, `OfficialLapTime`, `IsPartialStart` flag, and `[]SampleData`. |
| `LapKind` | `KindFlying`, `KindOutLap`, `KindInLap`, `KindOutInLap`. |
| `Phase` | Per-phase stats: entry/exit speed, brake%, peak brake, throttle%, TC%, avg lat G, peak steering angle, steering corrections, ABS, lockup/wheelspin, coast. |
| `SegZone` | Per-segment aggregate stats: entry/min/exit speed, brake%, peak brake, throttle%, gear, avg lat G, ABS count, coast, TC intervention%, lockup/wheelspin counts, avg tyre temps. |
| `Zone` | Per-zone stats for the legacy 20-zone split. |
| `SessionMeta` | Car, track, and driver name parsed from session YAML. |

### Key functions

```go
laps, err := analysis.ExtractLaps(ibtFile)
meta := analysis.ParseSessionMeta(yaml, "Ricky Maw")
trackLen := analysis.ParseTrackLength(yaml)
weather := analysis.ParseWeather(yaml)

phases := analysis.ComputePhases(&lap, segments)
zones := analysis.SegmentStats(&lap, segments)
entries := analysis.ComputeBrakeEntries(laps, segments)
```
