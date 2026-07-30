package main

// Lap selection and filtering, plus small formatting/path helpers for the
// analyze subcommand.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// lapMode describes how the -lap flag was resolved.
type lapMode int

const (
	lapModeBest lapMode = iota // empty / "0": best lap of the session
	lapModeNum                 // integer: that specific lap number in the .ibt
	lapModePB                  // "pb": render the stored PB lap from pb.json
)

// parseLapArg parses the -lap flag value. Empty/"0" → best, "pb" → PB, otherwise integer.
func parseLapArg(v string) (lapMode, int, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "0" {
		return lapModeBest, 0, nil
	}
	if strings.EqualFold(v, "pb") {
		return lapModePB, 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, 0, fmt.Errorf("-lap: %q is not a valid lap number, \"pb\", or empty", v)
	}
	return lapModeNum, n, nil
}

// dumpSegmentPath builds the output path for a -dump CSV. An empty dir yields
// a bare filename (current directory), which is what tests and a caller with
// no .ibt context expect.
func dumpSegmentPath(dir, segName string, lapNumber int) string {
	name := fmt.Sprintf("%s_lap%d.csv", segName, lapNumber)
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

// dumpSegmentAllLapsPath builds the output path for a -dump -dump-all CSV.
// The "_alllaps" suffix keeps it from colliding with a single-lap dump of the
// same segment, which has a different column set.
func dumpSegmentAllLapsPath(dir, segName string) string {
	name := fmt.Sprintf("%s_alllaps.csv", segName)
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

// segmentNames returns a comma-separated list of segment names for error messages.
func segmentNames(segs []trackmap.Segment) string {
	names := make([]string, len(segs))
	for i, s := range segs {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

// ---- helpers ----

// dashes returns a string of n dash characters for table separators.
func dashes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}

// lapTimeFilterDelta is the maximum number of seconds a lap may be slower than the
// session best before it is excluded from trackmap detection and brake-entry blending.
const lapTimeFilterDelta float32 = 1.5

// plausibleLapFraction is the fraction of the session median below which a
// flying lap is treated as anomalous (e.g. a stitched/partial LLT publish from
// iRacing) and rejected. 0.70 keeps any lap within 30% of typical pace.
const plausibleLapFraction float32 = 0.70

// plausibleLapMinTime returns a lower bound on plausible flying-lap times for
// this session. iRacing occasionally publishes an LLT value that's far shorter
// than a real lap (mid-session resets, partial recordings, telemetry hiccups);
// without a floor those surface as "flying laps" and get picked as the best.
// Returns 0 when there are too few laps (< 2) to derive a reference.
func plausibleLapMinTime(laps []analysis.Lap) float32 {
	var times []float32
	for i := range laps {
		l := &laps[i]
		if l.Kind != analysis.KindFlying || l.IsPartialStart || l.IsCut || l.LapTime <= 0 {
			continue
		}
		times = append(times, l.LapTime)
	}
	if len(times) < 2 {
		return 0
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	// Upper median (times[len/2]): with n=2 this picks the larger of the two,
	// so a single anomalously short lap can't drag the threshold down with it.
	median := times[len(times)/2]
	return median * plausibleLapFraction
}

// flyingLapsWithinTime returns flying, non-partial-start, non-cut laps whose
// lap time is within lapTimeFilterDelta of bestTime AND not anomalously short
// vs the session median. This excludes early slow laps, stitched/partial LLT
// values, and shortcut laps that would skew corner boundaries and brake-entry
// positions. Falls back to all plausible non-cut flying laps if none pass the
// time filter.
func flyingLapsWithinTime(laps []analysis.Lap, bestTime float32) []analysis.Lap {
	threshold := bestTime + lapTimeFilterDelta
	minTime := plausibleLapMinTime(laps)
	var result []analysis.Lap
	for i := range laps {
		l := &laps[i]
		if l.Kind != analysis.KindFlying || l.IsPartialStart || l.IsCut || l.LapTime <= 0 {
			continue
		}
		if l.LapTime < minTime {
			continue
		}
		if l.LapTime <= threshold {
			result = append(result, *l)
		}
	}
	if len(result) == 0 {
		// Fallback: return all plausible valid flying laps. bestLap itself
		// always passes since threshold = bestTime + delta >= bestTime.
		for i := range laps {
			l := &laps[i]
			if l.Kind != analysis.KindFlying || l.IsPartialStart || l.IsCut || l.LapTime <= 0 {
				continue
			}
			if l.LapTime < minTime {
				continue
			}
			result = append(result, *l)
		}
	}
	return result
}

// selectAnalyzeLap resolves which lap the output stage describes: the one named
// by -lap when given, otherwise the best flying lap of the session. Returns nil
// when the requested lap does not exist or no lap qualifies.
func selectAnalyzeLap(laps []analysis.Lap, lapNum int) *analysis.Lap {
	if lapNum > 0 {
		return findAnalyzeLap(laps, lapNum)
	}
	return bestAnalyzeLap(laps)
}

// consistencyLapFilterPct is how much slower than the session best a flying lap
// may be and still count towards the cross-lap views (consistency, -dump-all).
//
// This is deliberately far wider than lapTimeFilterDelta. That 1.5s window
// exists to keep corner *geometry* clean, where one sloppy lap genuinely
// corrupts the map. Spread is the opposite problem: filtering down to the laps
// that were already alike is what produces "you are perfectly consistent" from
// a session that was not. In practice the tight window often leaves a single
// lap, and a single lap has no spread at all.
//
// A percentage rather than a fixed delta so it scales with lap length — 10% is
// ~10s at Phillip Island and ~24s at the Nordschleife, which in both cases
// separates representative laps from an opening warm-up lap.
const consistencyLapFilterPct float32 = 0.10

// crossLapComparableLaps returns the laps used for cross-lap views: flying,
// non-cut, non-partial laps within consistencyLapFilterPct of bestTime, and not
// anomalously short against the session median.
func crossLapComparableLaps(laps []analysis.Lap, bestTime float32) []analysis.Lap {
	threshold := bestTime * (1 + consistencyLapFilterPct)
	minTime := plausibleLapMinTime(laps)
	var result []analysis.Lap
	for i := range laps {
		l := &laps[i]
		if l.Kind != analysis.KindFlying || l.IsPartialStart || l.IsCut || l.LapTime <= 0 {
			continue
		}
		if l.LapTime < minTime || l.LapTime > threshold {
			continue
		}
		result = append(result, *l)
	}
	return result
}

// lapNumbers returns the lap numbers of laps, for reporting which laps a
// cross-lap view was computed from.
func lapNumbers(laps []analysis.Lap) []int {
	out := make([]int, len(laps))
	for i := range laps {
		out[i] = laps[i].Number
	}
	return out
}

func findAnalyzeLap(laps []analysis.Lap, number int) *analysis.Lap {
	for i := range laps {
		if laps[i].Number == number {
			return &laps[i]
		}
	}
	return nil
}

func bestAnalyzeLap(laps []analysis.Lap) *analysis.Lap {
	minTime := plausibleLapMinTime(laps)
	var best *analysis.Lap
	for i := range laps {
		l := &laps[i]
		if l.Kind != analysis.KindFlying || l.IsPartialStart || l.IsCut {
			continue
		}
		if len(l.Samples) < analysis.MinSamplesForValidLap || l.LapTime <= 0 {
			continue
		}
		if l.LapTime < minTime {
			continue
		}
		if best == nil || l.LapTime < best.LapTime {
			best = l
		}
	}
	return best
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func analyzeDie(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "analyze: "+format+"\n", args...)
	os.Exit(1)
}

// nthLatestIbtFile returns the path of the nth most recently modified .ibt file
// in dir (1 = most recent). Returns an error if n exceeds the number of files.
func nthLatestIbtFile(dir string, n int) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	type ibtEntry struct {
		path    string
		modTime time.Time
	}
	var files []ibtEntry
	for _, e := range entries {
		// Case-insensitive: Windows filesystems are, so a "SESSION.IBT" would
		// otherwise be silently skipped and reported as "no .ibt files found".
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".ibt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, ibtEntry{
			path:    filepath.Join(dir, e.Name()),
			modTime: info.ModTime(),
		})
	}

	if len(files) == 0 {
		return "", fmt.Errorf("no .ibt files found in %s", dir)
	}

	// Sort descending by modification time (most recent first).
	sort.Slice(files, func(i, j int) bool {
		return files[j].modTime.Before(files[i].modTime)
	})

	if n > len(files) {
		return "", fmt.Errorf("file index %d out of range — only %d .ibt file(s) in %s", n, len(files), dir)
	}
	return files[n-1].path, nil
}

// ---- map summary line ----

// pluralize returns singular when n == 1, plural otherwise.
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// formatMapLine renders the "Map:" summary block (line plus trailing blank).
//
// existing is the stored track map when one was used, and nil when segments
// were detected fresh this session. matchScore < 0 means "not computed" —
// which happens whenever there was no comparable lap (no valid flying lap, or
// the session YAML carried no track length).
//
// The distinction matters: this used to branch on matchScore, so a stored map
// with no comparable lap fell through to the first-detection wording and
// reported a mature map as "geometry: low ... (first detection)", discarding
// both the real confidence and the stored GeoMethod. Branch on whether a
// stored map was used, and report the missing score as its own state.
func formatMapLine(segCount int, existing *trackmap.TrackMap, geomConf trackmap.GeometryConfidence, matchScore float32, detectedLaps int) string {
	if existing == nil {
		return fmt.Sprintf("Map:     %d segs [latlon] — geometry: low (%d %s, 1 session) — match: n/a (first detection)\n\n",
			segCount, detectedLaps, pluralize(detectedLaps, "lap", "laps"))
	}

	method := existing.GeoMethod
	if method == "" {
		method = "latlon"
	}
	matchStr := "n/a (no comparable lap)"
	if matchScore >= 0 {
		matchStr = fmt.Sprintf("%.0f%%", matchScore*100)
	}
	return fmt.Sprintf("Map:     %d segs [%s] — geometry: %s (%d %s, %d %s) — match: %s\n\n",
		segCount, method, geomConf,
		existing.LapsUsed, pluralize(existing.LapsUsed, "lap", "laps"),
		existing.SessionsUsed, pluralize(existing.SessionsUsed, "session", "sessions"),
		matchStr)
}
