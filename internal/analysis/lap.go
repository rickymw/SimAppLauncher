// Package analysis extracts per-lap statistics from iRacing .ibt telemetry.
package analysis

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rickymw/MotorHome/internal/ibt"
)

// SessionMeta holds car, track, and driver info extracted from the iRacing session YAML.
type SessionMeta struct {
	TrackDisplayName string
	CarScreenName    string
	DriverName       string // iRacing UserName of the player
}

// ParseSessionMeta extracts session metadata from the raw iRacing session info YAML.
//
// driverName is the player's iRacing UserName (from launcher.config.json "driver" field).
// When non-empty it is matched case-insensitively against UserName entries in the
// Drivers list so that multi-class sessions return the correct car.
// Falls back to DriverCarIdx, then to the first CarScreenName in the file.
func ParseSessionMeta(yaml, driverName string) SessionMeta {
	var carName, resolvedDriver string

	// Primary: match by UserName from config.
	if driverName != "" {
		resolvedDriver, carName = driverBlockByName(yaml, driverName)
	}

	// Fallback: use DriverCarIdx (the recording driver's own car index).
	if carName == "" {
		if idxStr := yamlField(yaml, "DriverCarIdx"); idxStr != "" {
			if idx, err := strconv.Atoi(idxStr); err == nil {
				resolvedDriver, carName = driverBlockByIdx(yaml, idx)
			}
		}
	}

	// Last resort: first CarScreenName in file.
	if carName == "" {
		carName = yamlField(yaml, "CarScreenName")
	}

	return SessionMeta{
		TrackDisplayName: yamlField(yaml, "TrackDisplayName"),
		CarScreenName:    carName,
		DriverName:       resolvedDriver,
	}
}

// driverBlockByName scans the Drivers list for a block whose UserName matches
// name (case-insensitive) and returns (UserName, CarScreenName).
func driverBlockByName(yaml, name string) (userName, carName string) {
	lines := strings.Split(yaml, "\n")
	inBlock := false
	var blockUser, blockCar string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- CarIdx:") {
			// Flush previous block if it matched.
			if inBlock && blockUser != "" && blockCar != "" {
				return blockUser, blockCar
			}
			inBlock = true
			blockUser, blockCar = "", ""
			continue
		}
		if !inBlock {
			continue
		}
		if strings.HasPrefix(trimmed, "UserName:") {
			blockUser = strings.TrimSpace(strings.TrimPrefix(trimmed, "UserName:"))
			if !strings.EqualFold(blockUser, name) {
				inBlock = false // wrong driver — skip this block
			}
			continue
		}
		if strings.HasPrefix(trimmed, "CarScreenName:") {
			blockCar = strings.TrimSpace(strings.TrimPrefix(trimmed, "CarScreenName:"))
		}
	}
	// Check final block.
	if inBlock && blockUser != "" && blockCar != "" {
		return blockUser, blockCar
	}
	return "", ""
}

// driverBlockByIdx scans the Drivers list for CarIdx == idx and returns
// (UserName, CarScreenName). Returns early once both fields are found so that
// a subsequent driver block cannot overwrite the results.
func driverBlockByIdx(yaml string, idx int) (userName, carName string) {
	target := fmt.Sprintf("- CarIdx: %d", idx)
	lines := strings.Split(yaml, "\n")
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- CarIdx:") {
			if inBlock {
				// We've moved past the matching block without finding all fields.
				return
			}
			inBlock = trimmed == target
			continue
		}
		if !inBlock {
			continue
		}
		if strings.HasPrefix(trimmed, "UserName:") {
			userName = strings.TrimSpace(strings.TrimPrefix(trimmed, "UserName:"))
		}
		if strings.HasPrefix(trimmed, "CarScreenName:") {
			carName = strings.TrimSpace(strings.TrimPrefix(trimmed, "CarScreenName:"))
		}
		if userName != "" && carName != "" {
			return // both fields captured — done
		}
	}
	return
}

// FormatLapTime formats a lap time in seconds as "M:SS.mmm".
func FormatLapTime(secs float32) string {
	if secs <= 0 {
		return "?:??.???"
	}
	total := int(secs)
	mins := total / 60
	wholeS := total % 60
	ms := int((secs-float32(total))*1000 + 0.5)
	if ms >= 1000 {
		ms = 999
	}
	return fmt.Sprintf("%d:%02d.%03d", mins, wholeS, ms)
}

// ParseWeather extracts air and track temperatures from the session YAML.
// Returns a string like "Air 27°C, Track 40°C", or "" if the fields are absent.
// TrackTemp is tried first; falls back to TrackSurfaceTemp (WeekendInfo field).
func ParseWeather(yaml string) string {
	parseTemp := func(key string) string {
		val := yamlField(yaml, key)
		if parts := strings.Fields(val); len(parts) >= 1 {
			if f, err := strconv.ParseFloat(parts[0], 64); err == nil {
				return fmt.Sprintf("%.0f°C", f)
			}
		}
		return ""
	}

	airTemp := parseTemp("AirTemp")

	trackTemp := parseTemp("TrackTemp")
	if trackTemp == "" {
		trackTemp = parseTemp("TrackSurfaceTemp")
	}

	switch {
	case airTemp != "" && trackTemp != "":
		return "Air " + airTemp + ", Track " + trackTemp
	case airTemp != "":
		return "Air " + airTemp
	case trackTemp != "":
		return "Track " + trackTemp
	default:
		return ""
	}
}

// ParseTrackLength extracts the track length in metres from the session YAML.
// iRacing format: "TrackLength: 6.02 km"
// Returns 0 if not found or unparseable.
func ParseTrackLength(yaml string) float64 {
	val := yamlField(yaml, "TrackLength")
	if val == "" {
		return 0
	}
	parts := strings.Fields(val)
	if len(parts) == 0 {
		return 0
	}
	f, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	if len(parts) >= 2 && strings.ToLower(parts[1]) == "km" {
		f *= 1000
	}
	return f
}

// yamlField extracts a value from iRacing's session info YAML by key name.
func yamlField(yaml, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return ""
}

// SampleData holds the subset of telemetry channels used for analysis.
// Units: Speed m/s, Accel m/s², SteeringAngle rad, SessionTime s.
type SampleData struct {
	LapDistPct    float32 // 0.0–1.0
	SessionTime   float64 // absolute seconds since session start
	Speed         float32 // m/s
	Throttle      float32 // 0.0–1.0
	Brake         float32 // 0.0–1.0
	Clutch        float32 // 0.0–1.0
	Gear          int32   // -1=R, 0=N, 1–8
	RPM           float32
	SteeringAngle float32 // rad
	LongAccel     float32 // m/s²; positive = forward
	LatAccel      float32 // m/s²; positive = left
	YawRate       float32 // rad/s
	ABSActive     bool
	Lat           float64 // decimal degrees; 0 if not available in telemetry
	Lon           float64 // decimal degrees; 0 if not available in telemetry
}

// LapKind classifies a lap based on how it starts and ends.
type LapKind int

const (
	KindFlying   LapKind = iota // complete S/F-to-S/F lap at racing speed
	KindOutLap                  // started from near standstill (pit/grid exit)
	KindInLap                   // ended at near standstill (pit entry)
	KindOutInLap                // started AND ended at near standstill
)

func (k LapKind) String() string {
	switch k {
	case KindOutLap:
		return "out lap"
	case KindInLap:
		return "in lap"
	case KindOutInLap:
		return "out/in lap"
	default:
		return "flying lap"
	}
}

// pitSpeedThreshold is the speed below which we consider the car stationary
// for out/in lap detection. 5 m/s ≈ 18 km/h.
const pitSpeedThreshold = float32(5.0)

// Lap holds all telemetry samples for one detected track lap.
//
// Lap boundaries are identified by a large backward jump in LapDistPct
// (>0.5), which corresponds to crossing the start/finish line.
//
// NOTE: iRacing's LapCurrentLapTime does NOT reset at the S/F line —
// it accumulates from when the lap started in the session. SessionTime
// is used for all timing calculations instead.
//
// LapTime is the duration of the portion captured in the recording.
// For a lap that started before the recording began, LapTime underestimates
// the true lap time; IsPartialStart flags this case.
type Lap struct {
	Number           int
	LapTime          float32 // seconds: SessionTime of last sample − first sample
	Kind             LapKind
	StartSessionTime float64 // SessionTime of first sample (for timeAtPct)
	IsPartialStart   bool    // true if recording started mid-lap (DistPct > 0.05)
	Samples          []SampleData
}

// sfDropThreshold is the minimum backward DistPct change that signals an S/F crossing.
const sfDropThreshold = float32(0.5)

// MinSamplesForValidLap is the minimum number of samples required to treat a
// lap segment as worth analysing. At 60 Hz this is 5 seconds of data.
const MinSamplesForValidLap = 300

// ExtractLaps reads every sample from f and splits them at S/F crossings
// (detected by LapDistPct dropping by more than 0.5 in one step).
//
// Single-sample artifacts (iRacing briefly sets Lap=0 at each S/F crossing)
// are absorbed into the adjacent lap rather than creating their own entry.
func ExtractLaps(f *ibt.File) ([]Lap, error) {
	n := f.NumSamples()
	if n == 0 {
		return nil, nil
	}

	var laps []Lap
	var cur *Lap
	var prevDist float32 = -1

	for i := 0; i < n; i++ {
		s, err := f.Sample(i)
		if err != nil {
			return nil, fmt.Errorf("analysis: reading sample %d: %w", i, err)
		}
		sd := extractSample(s)

		// Skip the single-sample Lap=0 artifact that iRacing emits at the
		// exact S/F crossing frame. It has DistPct=0.0000 but the surrounding
		// samples sit at ~0.03-0.04, so it's easily filtered.
		if i > 0 && prevDist > sfDropThreshold && sd.LapDistPct == 0 {
			// This is the crossing artifact frame. Start a new lap but don't
			// include this stale sample in either lap.
			if cur != nil && len(cur.Samples) >= MinSamplesForValidLap {
				finalizeLap(cur)
				laps = append(laps, *cur)
			}
			cur = &Lap{Number: len(laps) + 1}
			// Don't append this sample; prevDist stays as-is so next sample
			// is treated as the real start of the new lap.
			continue
		}

		// Normal S/F crossing (no zero-frame artifact):
		// DistPct jumps backward by more than 0.5 in consecutive samples.
		if prevDist >= 0 && prevDist-sd.LapDistPct > sfDropThreshold {
			if cur != nil && len(cur.Samples) >= MinSamplesForValidLap {
				finalizeLap(cur)
				laps = append(laps, *cur)
			}
			cur = &Lap{Number: len(laps) + 1}
		}

		if cur == nil {
			cur = &Lap{Number: 1}
		}

		if len(cur.Samples) == 0 {
			cur.StartSessionTime = sd.SessionTime
			cur.IsPartialStart = sd.LapDistPct > 0.05
		}
		cur.Samples = append(cur.Samples, sd)
		prevDist = sd.LapDistPct
	}

	// Append final (possibly incomplete) lap.
	if cur != nil && len(cur.Samples) > 0 {
		finalizeLap(cur)
		laps = append(laps, *cur)
	}

	return laps, nil
}

// finalizeLap sets LapTime and Kind from the samples.
func finalizeLap(lap *Lap) {
	if len(lap.Samples) == 0 {
		return
	}
	first := lap.Samples[0]
	last := lap.Samples[len(lap.Samples)-1]
	lap.LapTime = float32(last.SessionTime - first.SessionTime)

	slowStart := first.Speed < pitSpeedThreshold
	slowEnd := last.Speed < pitSpeedThreshold
	switch {
	case slowStart && slowEnd:
		lap.Kind = KindOutInLap
	case slowStart:
		lap.Kind = KindOutLap
	case slowEnd:
		lap.Kind = KindInLap
	default:
		lap.Kind = KindFlying
	}
}

// extractSample pulls analysis-relevant channels from an ibt.Sample.
// Missing channels (ok == false) result in zero values, which is safe.
func extractSample(s ibt.Sample) SampleData {
	var sd SampleData
	sd.LapDistPct, _ = s.Float32("LapDistPct")
	sd.SessionTime, _ = s.Float64("SessionTime")
	sd.Speed, _ = s.Float32("Speed")
	sd.Throttle, _ = s.Float32("Throttle")
	sd.Brake, _ = s.Float32("Brake")
	sd.Clutch, _ = s.Float32("Clutch")
	sd.Gear, _ = s.Int("Gear")
	sd.RPM, _ = s.Float32("RPM")
	sd.SteeringAngle, _ = s.Float32("SteeringWheelAngle")
	sd.LongAccel, _ = s.Float32("LongAccel")
	sd.LatAccel, _ = s.Float32("LatAccel")
	sd.YawRate, _ = s.Float32("YawRate")
	sd.ABSActive, _ = s.Bool("BrakeABSactive")
	sd.Lat, _ = s.Float64("Lat")
	sd.Lon, _ = s.Float64("Lon")
	return sd
}
