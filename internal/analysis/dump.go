package analysis

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// DumpConfig controls CSV dump output.
type DumpConfig struct {
	// DownsampleRate controls how many 60Hz samples to skip between output rows.
	// 3 = 20Hz output (every 3rd sample). 1 = full 60Hz.
	DownsampleRate int
	// ContextSamples is the number of extra samples to include before and after
	// the segment boundary (at the original 60Hz rate, before downsampling).
	ContextSamples int
}

// DefaultDumpConfig returns a config tuned for AI token efficiency:
// 20Hz output with 1 second of context on each side.
func DefaultDumpConfig() DumpConfig {
	return DumpConfig{
		DownsampleRate: 3,  // 60Hz → 20Hz
		ContextSamples: 60, // 1 second at 60Hz
	}
}

// ResolveSegmentName finds a segment by name (case-insensitive) or 1-based
// index string (e.g. "3" → segment index 2). Returns the segment index or -1.
func ResolveSegmentName(segs []trackmap.Segment, nameOrIdx string) int {
	// Try as 1-based index first.
	if idx := parseInt1Based(nameOrIdx); idx >= 0 && idx < len(segs) {
		return idx
	}
	// Case-insensitive name match.
	lower := strings.ToLower(nameOrIdx)
	for i, seg := range segs {
		if strings.ToLower(seg.Name) == lower {
			return i
		}
	}
	return -1
}

// parseInt1Based parses a string as a 1-based integer and returns the 0-based
// index. Returns -1 if the string is not a valid positive integer.
func parseInt1Based(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return -1
	}
	return n - 1
}

// DumpSegmentCSV writes a downsampled CSV of telemetry for a single segment
// to w. It includes ContextSamples of lead-in and lead-out from adjacent
// segments so the AI can see the approach and exit.
//
// The CSV has a comment header with segment metadata, then columns:
// Dist%,Time,Speed,Throttle,Brake,Steer,Gear,LatG,LongG,ABS,Coast
//
// Units: Speed km/h, Throttle/Brake 0–100, Steer degrees (+=left),
// LatG/LongG in g, ABS/Coast 0/1, Time seconds from first row.
func DumpSegmentCSV(w io.Writer, lap *Lap, segs []trackmap.Segment, segIdx int, cfg DumpConfig) error {
	if segIdx < 0 || segIdx >= len(segs) {
		return fmt.Errorf("segment index %d out of range (0–%d)", segIdx, len(segs)-1)
	}
	if cfg.DownsampleRate < 1 {
		cfg.DownsampleRate = 1
	}

	seg := segs[segIdx]

	// Compute effective entry/exit boundaries (same logic as ComputePhases).
	effEntry := make([]float32, len(segs))
	effExit := make([]float32, len(segs))
	for i, s := range segs {
		effEntry[i] = s.EntryPct // geometric entry — dump uses no brake-entry offset
		effExit[i] = s.ExitPct
	}
	for i := 0; i < len(segs)-1; i++ {
		if effEntry[i+1] < effExit[i] {
			effExit[i] = effEntry[i+1]
		}
	}

	// Find sample range: segment samples plus context.
	segStart, segEnd := -1, -1
	for i, s := range lap.Samples {
		inSeg := segmentForEffPct(s.LapDistPct, effEntry, effExit) == segIdx
		if inSeg {
			if segStart < 0 {
				segStart = i
			}
			segEnd = i
		}
	}
	if segStart < 0 {
		return fmt.Errorf("no samples found in segment %s", seg.Name)
	}

	// Expand range for context.
	rangeStart := segStart - cfg.ContextSamples
	if rangeStart < 0 {
		rangeStart = 0
	}
	rangeEnd := segEnd + cfg.ContextSamples
	if rangeEnd >= len(lap.Samples) {
		rangeEnd = len(lap.Samples) - 1
	}

	samples := lap.Samples[rangeStart : rangeEnd+1]

	// Write metadata comment.
	fmt.Fprintf(w, "# Segment: %s (%s)\n", seg.Name, seg.Kind)
	fmt.Fprintf(w, "# Lap: %d, Time: %s\n", lap.Number, FormatLapTime(lap.LapTime))
	fmt.Fprintf(w, "# Rate: %dHz (downsampled from 60Hz)\n", 60/cfg.DownsampleRate)
	fmt.Fprintf(w, "# Context: %d samples before/after segment boundary\n", cfg.ContextSamples/cfg.DownsampleRate)

	// Write header.
	fmt.Fprintln(w, "Dist%,Time,Speed,Throttle,Brake,Steer,Gear,LatG,LongG,ABS,Coast")

	// Write rows, downsampled.
	t0 := samples[0].SessionTime
	for i := 0; i < len(samples); i += cfg.DownsampleRate {
		s := samples[i]

		speedKPH := s.Speed * ms2kmh
		throttle := s.Throttle * 100
		brake := s.Brake * 100
		steerDeg := s.SteeringAngle * rad2deg
		latG := s.LatAccel / grav
		longG := s.LongAccel / grav
		abs := 0
		if s.ABSActive {
			abs = 1
		}
		coast := 0
		if s.Throttle < 0.05 && s.Brake < 0.05 {
			coast = 1
		}

		fmt.Fprintf(w, "%.4f,%.3f,%.1f,%.0f,%.0f,%.1f,%d,%.2f,%.2f,%d,%d\n",
			s.LapDistPct,
			s.SessionTime-t0,
			speedKPH,
			throttle,
			brake,
			steerDeg,
			s.Gear,
			latG,
			longG,
			abs,
			coast,
		)
	}

	return nil
}
