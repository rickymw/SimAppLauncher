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

**Lap timing:** iRacing publishes `LapLastLapTime` 0.1–1 second *after* the S/F crossing — at the crossing frame itself the channel still holds the previous lap's value (or `-1` for an invalidated lap). `ExtractLaps` therefore tracks LLT across samples; when it changes to a new positive value, that value is the official time of the most recently finalized lap and is stored as `OfficialLapTime` / `LapTime`. This matches the time shown in iRacing and third-party tools like Garage61.

Consequences:
- The final lap of a recording has no following lap to source LLT from, so it always falls back to `SessionTime[last] − SessionTime[first]` (within ~33 ms of the official time).
- Invalidated laps (track-limits violations) yield `LLT=-1` in iRacing, which is treated as missing — those laps also fall back to the SessionTime diff.
- A stale LLT carried over from a previous recording session does not leak onto our laps: the very first LLT sample seen never triggers an "update" because there is no `prevLLT` to compare against yet.

Out/in lap classification uses entry/exit speed: < 5 m/s at the first sample = out lap, < 5 m/s at the last sample = in lap.

**Cut detection:** flying laps are also checked for shortcut gaps in their `LapDistPct` coverage. The lap is binned into 100 1%-of-track buckets; if any contiguous run of 3+ buckets is empty (≈ 180 m skipped at Watkins Glen), the lap is marked `IsCut`. iRacing occasionally accepts a shortcut lap as valid (publishing a positive `LapLastLapTime` instead of `-1`), so this catches the case where a cut lap would otherwise be picked as the session best. The check is gated on `max(LapDistPct) >= 0.95` so that recordings truncated mid-lap don't false-trigger. Cut laps appear in the lap list with `[flying lap, cut]` and are excluded from best-lap selection, trackmap detection, brake-entry blending, the plausible-time floor, and PB updates.

### Brake entry detection (`zones.go`)

`ComputeBrakeEntries` scans flying laps backward from each corner's geometric entry to find the average point where brake pressure first exceeds 5%. A tolerance of 3 consecutive non-braking samples prevents ABS modulation from terminating the scan early. For the first corner (T1), the scan wraps around the S/F line to detect braking zones that start on the preceding straight (high LapDistPct near 1.0).

### Phase analysis (`phases.go`)

`ComputePhases` splits each segment into steering-based phases and computes per-phase statistics. Straights get one "full" phase. Corners are split into entry/mid/exit using the steering angle trace:

1. Find peak `|SteeringAngle|` across all samples in the segment
2. Entry: start → first sample reaching 80% of peak
3. Mid: samples at ≥ 80% of peak (committed to the arc)
4. Exit: last sample dropping below 80% of peak → end

Corners with peak steering < 5° get a single "full" phase. `countSteeringCorrections` detects rapid sign changes in steering rate within each phase.

### Exit-speed impact (`exitimpact.go`)

`ComputeExitImpact` pairs each corner/chicane with the straight segment immediately following it (wrapping from the last segment to the first, since the S/F straight typically follows the final corner) and reports the corner's exit speed (`Phase.SpeedExitKPH` of its last phase) alongside the peak speed reached on that straight (`Phase.PeakSpeedKPH`) — a direct measure of whether a slow exit cost speed down the next straight. Pairs with no computed phases on either side (e.g. a segment straddling a truncated final lap) are skipped.

### Sector times (`sectors.go`)

`ParseSectors` reads iRacing's own sector boundaries out of the session YAML's `SplitTimeInfo:` block (`SectorNum` / `SectorStartPct` pairs), so the sectors match the sim's timing rather than MotorHome's detected segments. Returns nil when the block is absent — some session types omit it.

`ComputeSectorTimes` returns the time spent in each sector on a lap. Boundary crossings are **linearly interpolated** between the samples either side rather than snapped to the nearest sample: at 60 Hz snapping would cost up to 17 ms per boundary, which is visible against lap times quoted to a millisecond. Sectors the lap doesn't fully span (out lap, partial start, recording stopped mid-lap) report `Complete: false` rather than a bogus time.

`BestSectorTimes` reduces per-lap sector times to the fastest in each sector plus the lap it came from; the sum is the theoretical best lap.

Note the sum of a lap's sectors differs from its official `LapLastLapTime` by a few ms (interpolation), so the CLI prints the official time in the lap column rather than the sum.

`ParseTrackNumTurns` reads iRacing's official turn count (`TrackNumTurns`) from the session YAML. It is **reported only, never used to drive detection** — it counts iRacing's turn labels rather than merged corner segments, so using it as a detection target would push detection to over-split corners. See the corner-labelling notes in [internal/trackmap/README.md](../trackmap/README.md).

### Segment CSV dump (`dump.go`)

`DumpSegmentCSV` writes a downsampled CSV of telemetry for a single segment, suitable for AI analysis. Output is 20Hz by default (every 3rd sample) with 1 second of context before/after the segment. Columns: `Dist%,Time,Speed,Throttle,Brake,Steer,Gear,LatG,LongG,ABS,Coast`. A typical corner produces ~200 rows — compact enough for direct AI consumption.

`ResolveSegmentName` finds a segment by name (case-insensitive, e.g. "T3") or 1-based index (e.g. "3").

`DumpSegmentAllLapsCSV` writes the same segment from several laps into one file, prefixed with a `Lap` column. Each lap's `Time` column restarts at 0 so the traces can be overlaid at equal time-into-the-corner; a session-relative clock would make that comparison impossible without further arithmetic. Laps with no samples in the segment are skipped (a truncated lap legitimately misses one); an error is returned only when no lap yielded any rows.

### Consistency across laps (`consistency.go`)

`ComputeConsistency` runs `ComputePhases` over a set of laps and reports, per (segment, phase), the mean and sample standard deviation (n−1) of entry speed, exit speed, peak brake, lateral G and coast time, plus the best exit speed seen and which lap set it. The phase table describes one lap; this describes how repeatable it is.

Pairs are matched by segment name + phase kind, the same key the vs-PB table uses. A corner can split into entry/mid/exit on one lap and collapse to a single `full` phase on another (peak steering below the 5° threshold), so the phases present are not identical from lap to lap — `Laps` records how many contributed to each row. Rows observed on fewer than `MinLapsForConsistency` (2) laps are dropped: one observation has no spread, and printing `± 0.0` for it would read as perfect consistency rather than as absent data.

`MostVariable` ranks rows by descending exit-speed spread. Exit speed is the ranking metric because it propagates — a corner exit varying by 5 km/h carries that variance down the whole following straight. It copies before sorting, so the caller's track-ordered slice is left intact.

Callers must pass an already-filtered set of comparable laps; mixing in an out lap would dominate every spread it touches.

### Fuel and stint planning (`fuel.go`)

`ComputeFuel` derives per-lap consumption from the `FuelLevel` channel and averages it over a caller-supplied population (the comparable laps — an out lap covers less than a full lap of track and would understate consumption).

Consumption is the difference between tank readings at each lap's boundaries, **not** an integration of `FuelUsePerHour`: the rate channel is an instantaneous reading and integrating it accumulates error across a lap, while the level difference is what actually went into the engine. The rate is still reported as an average, but nothing is derived from it.

Session endpoints are deliberately not used for totals. A session that ends with a pit stop has a full tank in its final sample, so `StartLitres - lastSample` reports a completed stint as having used nothing. `UsedLitres` is the sum of per-lap consumption, and `EndLitres` is measured at the end of the last lap that did not refuel. A lap that gained more than `fuelRefuelThreshold` litres is flagged `Refuelled`, has its consumption zeroed (burn and fill cannot be separated within one lap), and is excluded from the averages.

Both an average and a worst-lap figure are reported, because planning a stint on the average runs dry half the time. `FuelForLaps` sizes a plan with a margin expressed in laps rather than a percentage — that is the granularity the decision is actually made at.

A flat or absent channel yields `Available: false`; callers must not render the other fields in that case.

### Locating voice notes (`notes.go`)

`LocateNotes` maps each voice note's wall-clock timestamp onto the lap and segment being driven at that moment, and returns the notes sorted by time.

The mapping is anchored on the **first telemetry sample** rather than the disk header's `SessionStartTime` field: the first sample's `SessionTime` is by definition the start of the recording, so the arithmetic holds regardless of how that header field is interpreted.

`lag` is subtracted from each note's timestamp before locating it — see `DefaultNoteLag` (2s). Zero would be a worse default than an estimate: a note about a corner can never be recorded before that corner happened, so an uncorrected timestamp is guaranteed to land late, typically in the following straight. Because it is an estimate rather than a measurement, it is a parameter (`analyze -note-lag`) rather than a constant.

Notes falling outside the recording come back with `Located == false` and keep their `Text`; only the position fields are meaningless.

### Setup comparison (`setup.go`)

`FlattenSetup` walks a parsed `CarSetup` tree into `{Path, Value}` leaves in document order (`Chassis/LeftFront/Camber`). `DiffSetups` compares two flattened setups and returns the fields that differ, in the order they appear in the newer one. Fields present on only one side are reported with the missing side empty rather than skipped — that usually means a different car or iRacing build, and hiding it would make the diff look like a small tweak.

`FilterSessionState` drops leaves that record what happened during the session rather than what the driver set (`UpdateCount`, `LastHotPressure`, `LastTemps*`, `TreadRemaining`). iRacing writes end-of-session tyre readings back into the setup block, so a raw diff of two *identical* setups still reports every corner's pressure, temperature and tread. Filtering them is what makes the diff readable; the count of hidden entries is returned rather than discarded so callers can say how much was suppressed.

### Legacy zone stats

`ZoneStats` divides the track into 20 equal 5% zones. Retained but not used by the CLI.

## Architecture

| Symbol | Description |
|---|---|
| `SampleData` | ~60 telemetry channels per sample: timing, driver inputs (raw & processed), dynamics, driver aids, wheel speeds, tyre temps/wear/pressure, brake line pressures, fuel, steering torque. |
| `Lap` | One lap: number, time (`LapLastLapTime` preferred; SessionTime diff fallback), kind, `OfficialLapTime`, `IsPartialStart` and `IsCut` flags, and `[]SampleData`. |
| `LapKind` | `KindFlying`, `KindOutLap`, `KindInLap`, `KindOutInLap`. |
| `Phase` | Per-phase stats: entry/exit/peak speed, brake%, peak brake, throttle%, avg lat G, peak steering angle, steering corrections, ABS, lockup/wheelspin, coast. |
| `ExitImpact` | Corner exit speed paired with the peak speed reached on the following straight, for one lap. |
| `DumpConfig` | Controls CSV dump: downsample rate (default 3 = 20Hz) and context samples (default 60 = 1s). |
| `ConsistencyRow` | Per-(segment, phase) spread across laps: mean/SD of entry speed, exit speed, peak brake, lat G and coast, plus best exit speed and the lap that set it. |
| `FuelSummary` / `LapFuel` | Per-lap and session fuel consumption, remaining litres, laps of headroom at average and worst-lap rates. `Available` is false when the channel is absent. |
| `NoteInput` / `LocatedNote` | A voice note awaiting placement, and the same note resolved to a lap, lap distance and segment (`Located` false when it falls outside the recording). |
| `SetupValue` / `SetupDiffEntry` | A flattened `CarSetup` leaf (`Path`, `Value`), and one difference between two setups (`Path`, `Old`, `New`). |
| `Zone` | Per-zone stats for the legacy 20-zone split. |
| `SessionMeta` | Car, track, and driver name parsed from session YAML. |
| `TyreSummary` / `CornerTyres` | Per-corner avg surface (tread) temps (inner/outer mapped from iRacing tempL/tempR accounting for left- vs right-side), end-of-lap wear, avg hot pressure, and brake bias for one lap. Uses surface temp rather than iRacing's carcass-temp channels, which freeze at a stale value for entire sessions on some cars. |

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
exitImpacts := analysis.ComputeExitImpact(segments, phases)

// Dump a corner's telemetry to CSV for AI analysis.
segIdx := analysis.ResolveSegmentName(segments, "T3")
analysis.DumpSegmentCSV(writer, &lap, segments, segIdx, analysis.DefaultDumpConfig())

// Same corner across several laps, in one file with a Lap column.
analysis.DumpSegmentAllLapsCSV(writer, comparableLaps, segments, segIdx, analysis.DefaultDumpConfig())

// Lap-to-lap spread, and the phases that vary most.
rows := analysis.ComputeConsistency(comparableLaps, segments, brakeEntries)
worst := analysis.MostVariable(rows, 3)

// Fuel use and stint headroom.
fuel := analysis.ComputeFuel(laps, comparableLaps)
needed := analysis.FuelForLaps(fuel.WorstPerLap, 12, 1) // 12 laps + 1 lap margin

// Place voice notes on track.
located := analysis.LocateNotes(laps, segments, recStart, analysis.DefaultNoteLag, noteInputs)

// Compare two car setups.
diff := analysis.DiffSetups(
    analysis.FlattenSetup(analysis.ParseCarSetupTree(pbEntry.Setup)),
    analysis.FlattenSetup(analysis.ParseCarSetupTree(sessionYAML)),
)
diff, hidden := analysis.FilterSessionState(diff)
```
