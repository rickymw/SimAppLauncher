# internal/analysis

Extracts per-lap statistics from iRacing `.ibt` telemetry samples.

## What it does

- Splits a raw sample stream into `Lap` objects at start/finish crossings
- Classifies each lap as flying, out lap, in lap, or out/in lap
- Computes per-phase statistics (speed, inputs, G-forces, steering metrics, ABS, coasting, lockup/wheelspin) by splitting corners into entry/mid/exit phases using steering angle
- Detects average braking-onset distance for each corner/chicane

## How it works

### Lap extraction (`lap.go`)

`ExtractLaps` scans all samples and splits at S/F crossings: any step where `LapDistPct` drops by more than 0.5. A single-sample artifact (iRacing briefly sets `LapDistPct=0` at the exact crossing frame) is absorbed rather than creating a spurious extra lap. Laps shorter than 300 samples (5 s at 60 Hz) are discarded.

**Lap timing:** `LapLastLapTime` is read from the S/F crossing frame (the artifact frame in the zero-artifact case, or the first frame of the new lap in the normal case). When present and > 0 it is stored as `OfficialLapTime` and used as `LapTime` — matching the time shown in iRacing and third-party tools like Garage61. If the channel is absent, `LapTime` falls back to `SessionTime[last] − SessionTime[first]`, which can differ by up to ~33 ms per boundary.

Out/in lap classification uses entry/exit speed: < 5 m/s at the first sample = out lap, < 5 m/s at the last sample = in lap.

### Brake entry detection (`zones.go`)

`ComputeBrakeEntries` scans flying laps backward from each corner's geometric entry to find the average point where brake pressure first exceeds 5%. A tolerance of 3 consecutive non-braking samples prevents ABS modulation from terminating the scan early. For the first corner (T1), the scan wraps around the S/F line to detect braking zones that start on the preceding straight (high LapDistPct near 1.0).

### Phase analysis (`phases.go`)

`ComputePhases` splits each segment into steering-based phases and computes per-phase statistics. Straights get one "full" phase. Corners are split into entry/mid/exit using the steering angle trace:

1. Find peak `|SteeringAngle|` across all samples in the segment
2. Entry: start → first sample reaching 80% of peak
3. Mid: samples at ≥ 80% of peak (committed to the arc)
4. Exit: last sample dropping below 80% of peak → end

Corners with peak steering < 5° get a single "full" phase. `countSteeringCorrections` detects rapid sign changes in steering rate within each phase.

### Segment CSV dump (`dump.go`)

`DumpSegmentCSV` writes a downsampled CSV of telemetry for a single segment, suitable for AI analysis. Output is 20Hz by default (every 3rd sample) with 1 second of context before/after the segment. Columns: `Dist%,Time,Speed,Throttle,Brake,Steer,Gear,LatG,LongG,ABS,Coast`. A typical corner produces ~200 rows — compact enough for direct AI consumption.

`ResolveSegmentName` finds a segment by name (case-insensitive, e.g. "T3") or 1-based index (e.g. "3").

### Legacy zone stats

`ZoneStats` divides the track into 20 equal 5% zones. Retained but not used by the CLI.

## Architecture

| Symbol | Description |
|---|---|
| `SampleData` | ~60 telemetry channels per sample: timing, driver inputs (raw & processed), dynamics, driver aids, wheel speeds, tyre temps/wear/pressure, brake line pressures, fuel, steering torque. |
| `Lap` | One lap: number, time (`LapLastLapTime` preferred; SessionTime diff fallback), kind, `OfficialLapTime`, `IsPartialStart` flag, and `[]SampleData`. |
| `LapKind` | `KindFlying`, `KindOutLap`, `KindInLap`, `KindOutInLap`. |
| `Phase` | Per-phase stats: entry/exit speed, brake%, peak brake, throttle%, avg lat G, peak steering angle, steering corrections, ABS, lockup/wheelspin, coast. |
| `DumpConfig` | Controls CSV dump: downsample rate (default 3 = 20Hz) and context samples (default 60 = 1s). |
| `Zone` | Per-zone stats for the legacy 20-zone split. |
| `SessionMeta` | Car, track, and driver name parsed from session YAML. |
| `TyreSummary` / `CornerTyres` | Per-corner avg carcass temps (inner/outer mapped from iRacing CL/CR accounting for left- vs right-side), end-of-lap wear, avg hot pressure, and brake bias for one lap. |

### Key functions

```go
laps, err := analysis.ExtractLaps(ibtFile)
meta := analysis.ParseSessionMeta(yaml, "Ricky Maw")
tyreSummary := analysis.ComputeTyreSummary(&lap)
carSetup := analysis.ParseCarSetup(yaml)
trackLen := analysis.ParseTrackLength(yaml)
weather := analysis.ParseWeather(yaml)

phases := analysis.ComputePhases(&lap, segments)
entries := analysis.ComputeBrakeEntries(laps, segments)

// Dump a corner's telemetry to CSV for AI analysis.
segIdx := analysis.ResolveSegmentName(segments, "T3")
analysis.DumpSegmentCSV(writer, &lap, segments, segIdx, analysis.DefaultDumpConfig())
```
