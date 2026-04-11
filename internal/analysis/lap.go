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
// Arithmetic is done in float64 to avoid rounding errors near .999/.000
// boundaries that float32's ~7 significant digits can cause.
func FormatLapTime(secs float32) string {
	if secs <= 0 {
		return "?:??.???"
	}
	s := float64(secs)
	total := int(s)
	mins := total / 60
	wholeS := total % 60
	ms := int((s-float64(total))*1000 + 0.5)
	if ms >= 1000 {
		wholeS++
		ms = 0
		if wholeS >= 60 {
			mins++
			wholeS = 0
		}
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
// Strips surrounding quotes since iRacing occasionally quotes string values.
// NOTE: internal/iracing/live_windows.go has a duplicate — keep behaviour in sync.
func yamlField(yaml, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			val := strings.TrimSpace(trimmed[len(prefix):])
			val = strings.Trim(val, "\"'")
			return val
		}
	}
	return ""
}

// SampleData holds telemetry channels used for analysis.
// Units: Speed/WheelSpeed m/s, Accel m/s², SteeringAngle rad, SessionTime s,
// Temps °C, Pressures kPa, BrakeLinePress bar, Fuel litres, Wear 0–1.
type SampleData struct {
	// Core timing & position
	LapDistPct  float32 // 0.0–1.0
	SessionTime float64 // absolute seconds since session start
	Speed       float32 // m/s
	Lat         float64 // decimal degrees; 0 if not available
	Lon         float64 // decimal degrees; 0 if not available

	// Driver inputs (processed by car systems)
	Throttle      float32 // 0.0–1.0 (after TC)
	Brake         float32 // 0.0–1.0 (after ABS)
	Clutch        float32 // 0.0–1.0
	Gear          int32   // -1=R, 0=N, 1–8
	SteeringAngle float32 // rad

	// Raw driver inputs (before car systems)
	ThrottleRaw float32 // 0.0–1.0 (pedal position, before TC)
	BrakeRaw    float32 // 0.0–1.0 (pedal position, before ABS)

	// Engine
	RPM float32

	// Vehicle dynamics
	LongAccel float32 // m/s²; positive = forward
	LatAccel  float32 // m/s²; positive = left
	YawRate   float32 // rad/s

	// Driver aids
	ABSActive  bool
	ABSCutPct  float32 // 0–1; fraction of brake cut by ABS
	BrakeBias  float32 // driver-adjustable brake bias
	TCSetting  float32 // traction control level
	TCSetting2 float32 // traction control level 2 (some cars)
	ABSSetting float32 // ABS level

	// Wheel speeds (m/s)
	LFspeed float32
	RFspeed float32
	LRspeed float32
	RRspeed float32

	// Tyre carcass temperatures (°C) — inner/middle/outer across tread
	LFtempCL, LFtempCM, LFtempCR float32
	RFtempCL, RFtempCM, RFtempCR float32
	LRtempCL, LRtempCM, LRtempCR float32
	RRtempCL, RRtempCM, RRtempCR float32

	// Tyre wear (0–1, 1 = new)
	LFwearL, LFwearM, LFwearR float32
	RFwearL, RFwearM, RFwearR float32
	LRwearL, LRwearM, LRwearR float32
	RRwearL, RRwearM, RRwearR float32

	// Tyre pressures (kPa, hot)
	LFpressure, RFpressure, LRpressure, RRpressure float32

	// Brake line pressures (bar, per corner)
	LFbrakeLinePress, RFbrakeLinePress float32
	LRbrakeLinePress, RRbrakeLinePress float32

	// Fuel
	FuelLevel      float32 // litres
	FuelUsePerHour float32 // kg/h

	// Steering feedback
	SteeringWheelTorque float32 // N·m
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
// LapTime is authoritative: it is set from LapLastLapTime (the game's
// official time, captured at the S/F crossing) when that channel is present,
// otherwise it falls back to SessionTime[last] − SessionTime[first].
// IsPartialStart flags laps where recording began mid-lap; those are excluded
// from best-lap selection.
type Lap struct {
	Number           int
	LapTime          float32 // seconds: official (LapLastLapTime) or SessionTime diff
	OfficialLapTime  float32 // raw LapLastLapTime from the crossing frame; 0 if absent
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
			// This is the crossing artifact frame. iRacing updates LapLastLapTime
			// on this frame — capture it before discarding the sample.
			if cur != nil {
				if lt, ok := s.Float32("LapLastLapTime"); ok && lt > 0 {
					cur.OfficialLapTime = lt
				}
				if len(cur.Samples) >= MinSamplesForValidLap {
					finalizeLap(cur)
					laps = append(laps, *cur)
				}
			}
			cur = &Lap{Number: len(laps) + 1}
			// Don't append this sample; prevDist stays as-is so next sample
			// is treated as the real start of the new lap.
			continue
		}

		// Normal S/F crossing (no zero-frame artifact):
		// DistPct jumps backward by more than 0.5 in consecutive samples.
		// LapLastLapTime is updated on the first frame of the new lap (sd here).
		if prevDist >= 0 && prevDist-sd.LapDistPct > sfDropThreshold {
			if cur != nil {
				if lt, ok := s.Float32("LapLastLapTime"); ok && lt > 0 {
					cur.OfficialLapTime = lt
				}
				if len(cur.Samples) >= MinSamplesForValidLap {
					finalizeLap(cur)
					laps = append(laps, *cur)
				}
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
// LapTime prefers OfficialLapTime (from LapLastLapTime at the S/F crossing)
// over the SessionTime diff, which misses fractional time at the boundaries.
func finalizeLap(lap *Lap) {
	if len(lap.Samples) == 0 {
		return
	}
	first := lap.Samples[0]
	last := lap.Samples[len(lap.Samples)-1]
	if lap.OfficialLapTime > 0 {
		lap.LapTime = lap.OfficialLapTime
	} else {
		lap.LapTime = float32(last.SessionTime - first.SessionTime)
	}

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

	// Core timing & position
	sd.LapDistPct, _ = s.Float32("LapDistPct")
	sd.SessionTime, _ = s.Float64("SessionTime")
	sd.Speed, _ = s.Float32("Speed")
	sd.Lat, _ = s.Float64("Lat")
	sd.Lon, _ = s.Float64("Lon")

	// Driver inputs (processed)
	sd.Throttle, _ = s.Float32("Throttle")
	sd.Brake, _ = s.Float32("Brake")
	sd.Clutch, _ = s.Float32("Clutch")
	sd.Gear, _ = s.Int("Gear")
	sd.SteeringAngle, _ = s.Float32("SteeringWheelAngle")

	// Raw driver inputs (before car systems)
	sd.ThrottleRaw, _ = s.Float32("ThrottleRaw")
	sd.BrakeRaw, _ = s.Float32("BrakeRaw")

	// Engine
	sd.RPM, _ = s.Float32("RPM")

	// Vehicle dynamics
	sd.LongAccel, _ = s.Float32("LongAccel")
	sd.LatAccel, _ = s.Float32("LatAccel")
	sd.YawRate, _ = s.Float32("YawRate")

	// Driver aids
	sd.ABSActive, _ = s.Bool("BrakeABSactive")
	sd.ABSCutPct, _ = s.Float32("BrakeABScutPct")
	sd.BrakeBias, _ = s.Float32("dcBrakeBias")
	sd.TCSetting, _ = s.Float32("dcTractionControl")
	sd.TCSetting2, _ = s.Float32("dcTractionControl2")
	sd.ABSSetting, _ = s.Float32("dcABS")

	// Wheel speeds
	sd.LFspeed, _ = s.Float32("LFspeed")
	sd.RFspeed, _ = s.Float32("RFspeed")
	sd.LRspeed, _ = s.Float32("LRspeed")
	sd.RRspeed, _ = s.Float32("RRspeed")

	// Tyre carcass temperatures
	sd.LFtempCL, _ = s.Float32("LFtempCL")
	sd.LFtempCM, _ = s.Float32("LFtempCM")
	sd.LFtempCR, _ = s.Float32("LFtempCR")
	sd.RFtempCL, _ = s.Float32("RFtempCL")
	sd.RFtempCM, _ = s.Float32("RFtempCM")
	sd.RFtempCR, _ = s.Float32("RFtempCR")
	sd.LRtempCL, _ = s.Float32("LRtempCL")
	sd.LRtempCM, _ = s.Float32("LRtempCM")
	sd.LRtempCR, _ = s.Float32("LRtempCR")
	sd.RRtempCL, _ = s.Float32("RRtempCL")
	sd.RRtempCM, _ = s.Float32("RRtempCM")
	sd.RRtempCR, _ = s.Float32("RRtempCR")

	// Tyre wear
	sd.LFwearL, _ = s.Float32("LFwearL")
	sd.LFwearM, _ = s.Float32("LFwearM")
	sd.LFwearR, _ = s.Float32("LFwearR")
	sd.RFwearL, _ = s.Float32("RFwearL")
	sd.RFwearM, _ = s.Float32("RFwearM")
	sd.RFwearR, _ = s.Float32("RFwearR")
	sd.LRwearL, _ = s.Float32("LRwearL")
	sd.LRwearM, _ = s.Float32("LRwearM")
	sd.LRwearR, _ = s.Float32("LRwearR")
	sd.RRwearL, _ = s.Float32("RRwearL")
	sd.RRwearM, _ = s.Float32("RRwearM")
	sd.RRwearR, _ = s.Float32("RRwearR")

	// Tyre pressures
	sd.LFpressure, _ = s.Float32("LFpressure")
	sd.RFpressure, _ = s.Float32("RFpressure")
	sd.LRpressure, _ = s.Float32("LRpressure")
	sd.RRpressure, _ = s.Float32("RRpressure")

	// Brake line pressures
	sd.LFbrakeLinePress, _ = s.Float32("LFbrakeLinePress")
	sd.RFbrakeLinePress, _ = s.Float32("RFbrakeLinePress")
	sd.LRbrakeLinePress, _ = s.Float32("LRbrakeLinePress")
	sd.RRbrakeLinePress, _ = s.Float32("RRbrakeLinePress")

	// Fuel
	sd.FuelLevel, _ = s.Float32("FuelLevel")
	sd.FuelUsePerHour, _ = s.Float32("FuelUsePerHour")

	// Steering feedback
	sd.SteeringWheelTorque, _ = s.Float32("SteeringWheelTorque")

	return sd
}
