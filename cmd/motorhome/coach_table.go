package main

// Turn-by-turn coaching table (`coach -table`).
//
// This is a second renderer over the same analyzeResult the JSON brief carries,
// not a second analysis — the same reason analyze_json.go exists alongside the
// ASCII tables. `coach` with no flags is written for an assistant to read;
// `-table` collapses the same content to one row per corner for a human to scan
// between runs, without the phase table's 13 columns.
//
// The Grade column is a triage hint, not a measurement. The tool has no model of
// what a good corner is; the letter only counts how many of five fixed
// thresholds a corner trips. Those thresholds are heuristics — they separate
// "worth looking at" from "fine" on the sessions they were calibrated against
// and nothing more. They are printed in the legend beneath the table so a reader
// can see what produced a letter instead of having to trust it, and so a grade
// that disagrees with the driver's own sense of the corner can be argued with.
//
// Deciding *what to do* about a flagged corner is deliberately not here. That is
// the coaching judgement the assistant reading `coach` supplies; a "Fix" column
// generated from thresholds would be a canned string dressed up as advice.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// Flag thresholds. Deliberately round numbers: they are triage cutoffs, and
// false precision would imply they were derived from something.
const (
	// coachCoastFlagSeconds — three quarters of a second with neither pedal
	// applied is ~20 m at hairpin speed and considerably more elsewhere.
	coachCoastFlagSeconds = 0.75
	// coachLockFlagSeconds — brief lock under threshold braking is normal;
	// this much means the zone is being driven on the ABS rather than at it.
	coachLockFlagSeconds = 1.5
	// coachSpinFlagSeconds — sustained wheelspin, as distinct from a chirp on
	// power application.
	coachSpinFlagSeconds = 0.75
	// coachExitSDFlagKPH — lap-to-lap exit speed spread. Exit speed propagates
	// down the following straight, so variance here is multiplied.
	coachExitSDFlagKPH = 8.0
	// coachCorrectionsFlag — steering reversals within one corner.
	coachCorrectionsFlag = 3
)

// samplesPerSecond converts the phase sample counts (ABS, lockup, wheelspin) to
// seconds. The .ibt tick rate is carried on the result, but every consumer of
// these counts has assumed 60 Hz since they were introduced; using the actual
// rate here would silently change existing numbers.
const samplesPerSecond = 60.0

// coachTurnRow is one corner, aggregated across its entry/mid/exit phases.
type coachTurnRow struct {
	Name     string
	SpeedIn  float32
	SpeedMin float32
	SpeedOut float32
	Coast    float32 // seconds
	Lock     float32 // seconds
	Spin     float32 // seconds
	Corr     int
	// ExitSD is the largest lap-to-lap exit-speed spread across the corner's
	// phases, and Laps how many laps contributed. Laps is 0 when there was no
	// consistency data, which is distinct from a measured spread of zero.
	ExitSD float32
	Laps   int

	Flags []string
	Grade string
}

// buildCoachTurnRows aggregates the analysed lap's phases into one row per
// corner, in track order.
//
// Straights are dropped: they carry no coaching signal of their own (a straight
// is either flat or it is a segment-boundary artifact), and including them would
// double the table's length. Their contribution is not lost — braking that
// begins on a straight is reported against the corner it belongs to only in the
// phase table, which is why the legend points there.
func buildCoachTurnRows(res analyzeResult) []coachTurnRow {
	if res.AnalysedLap == nil {
		return nil
	}

	corner := cornerSegmentNames(res)

	// Preserve track order: phases arrive in segment order, so first-seen wins.
	var order []string
	byName := map[string]*coachTurnRow{}
	for _, p := range res.AnalysedLap.Phases {
		if !corner(p.Segment, p.Phase) {
			continue
		}
		r, ok := byName[p.Segment]
		if !ok {
			r = &coachTurnRow{Name: p.Segment, SpeedIn: p.SpeedEntryKPH, SpeedMin: p.SpeedEntryKPH}
			byName[p.Segment] = r
			order = append(order, p.Segment)
		}
		r.SpeedOut = p.SpeedExitKPH
		r.SpeedMin = min32(r.SpeedMin, min32(p.SpeedEntryKPH, p.SpeedExitKPH))
		r.Coast += p.CoastSeconds
		r.Lock += float32(p.Lockups) / samplesPerSecond
		r.Spin += float32(p.Wheelspin) / samplesPerSecond
		r.Corr += p.Corrections
	}

	// Worst spread across the corner's phases: one badly repeated phase is the
	// finding, and averaging it against two steady ones would hide it.
	for _, c := range res.Consistency {
		r, ok := byName[c.Segment]
		if !ok {
			continue
		}
		if c.ExitSpeedSD > r.ExitSD {
			r.ExitSD = c.ExitSpeedSD
		}
		if c.Laps > r.Laps {
			r.Laps = c.Laps
		}
	}

	rows := make([]coachTurnRow, 0, len(order))
	for _, name := range order {
		r := byName[name]
		r.Flags, r.Grade = gradeCoachTurn(*r)
		rows = append(rows, *r)
	}
	return rows
}

// cornerSegmentNames returns a predicate reporting whether a phase belongs to a
// corner.
//
// The track map's segment kinds are authoritative, but trimForCoaching drops the
// geometry, so the predicate falls back to the phase name: ComputePhases splits
// corners into entry/mid/exit and gives straights a single "full" phase. That
// fallback misses corners taken with under 5 degrees of steering (which also
// collapse to "full"), so it is only used when the geometry is unavailable.
func cornerSegmentNames(res analyzeResult) func(segment, phase string) bool {
	if res.TrackMap != nil && len(res.TrackMap.Segments) > 0 {
		corners := map[string]bool{}
		for _, s := range res.TrackMap.Segments {
			if s.Kind == trackmap.KindCorner || s.Kind == trackmap.KindChicane {
				corners[s.Name] = true
			}
		}
		return func(segment, _ string) bool { return corners[segment] }
	}
	return func(_, phase string) bool { return phase != "full" }
}

// gradeCoachTurn applies the flag thresholds and reduces them to a letter.
func gradeCoachTurn(r coachTurnRow) ([]string, string) {
	var flags []string
	if r.Coast > coachCoastFlagSeconds {
		flags = append(flags, "coast")
	}
	if r.Lock > coachLockFlagSeconds {
		flags = append(flags, "lock")
	}
	if r.Spin > coachSpinFlagSeconds {
		flags = append(flags, "spin")
	}
	// Only flag spread that was actually measured. With one lap there is no
	// spread to measure, and reporting that as a clean corner would be a lie of
	// omission — the legend says so instead.
	if r.Laps >= 2 && r.ExitSD > coachExitSDFlagKPH {
		flags = append(flags, "spread")
	}
	if r.Corr >= coachCorrectionsFlag {
		flags = append(flags, "corr")
	}

	grades := []string{"A", "B", "C", "D", "F"}
	i := len(flags)
	if i >= len(grades) {
		i = len(grades) - 1
	}
	return flags, grades[i]
}

// writeCoachTable renders the turn-by-turn view.
func writeCoachTable(w io.Writer, res analyzeResult) {
	rows := buildCoachTurnRows(res)
	if len(rows) == 0 {
		fmt.Fprintln(w, "No corners to report: the session has no track map, so there are")
		fmt.Fprintln(w, "no named segments to aggregate. Run `motorhome analyze -update-map`.")
		fmt.Fprintln(w)
		return
	}

	if res.AnalysedLap != nil {
		fmt.Fprintf(w, "Turn-by-turn — lap %d (%s)\n\n",
			res.AnalysedLap.Number, res.AnalysedLap.TimeFormatted)
	}

	nameW := len("Turn")
	for _, r := range rows {
		if len(r.Name) > nameW {
			nameW = len(r.Name)
		}
	}
	flagW := len("Flags")
	for _, r := range rows {
		if n := len(strings.Join(r.Flags, " ")); n > flagW {
			flagW = n
		}
	}

	const speedW = 16 // "Speed in>min>out"

	fmt.Fprintf(w, " %-*s | %-*s | %-5s | %-5s | %-5s | %-6s | %-*s | %s\n",
		nameW, "Turn", speedW, "Speed in>min>out", "Coast", "Lock", "Spin", "ExitSD", flagW, "Flags", "Grade")
	fmt.Fprintf(w, "-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|-%s-|------\n",
		strings.Repeat("-", nameW), strings.Repeat("-", speedW), strings.Repeat("-", 5),
		strings.Repeat("-", 5), strings.Repeat("-", 5), strings.Repeat("-", 6),
		strings.Repeat("-", flagW))

	for _, r := range rows {
		speed := fmt.Sprintf("%.0f>%.0f>%.0f", r.SpeedIn, r.SpeedMin, r.SpeedOut)
		sd := "  -"
		if r.Laps >= 2 {
			sd = fmt.Sprintf("%.1f", r.ExitSD)
		}
		fmt.Fprintf(w, " %-*s | %-*s | %5s | %5s | %5s | %6s | %-*s | %s\n",
			nameW, r.Name, speedW, speed,
			coachSeconds(r.Coast), coachSeconds(r.Lock), coachSeconds(r.Spin),
			sd, flagW, strings.Join(r.Flags, " "), r.Grade)
	}

	fmt.Fprintln(w)
	writeCoachTableLegend(w, rows)
	writeCoachSectorLoss(w, res)
}

// writeCoachTableLegend states the thresholds behind the Grade column, so a
// letter can be argued with rather than taken on faith.
func writeCoachTableLegend(w io.Writer, rows []coachTurnRow) {
	fmt.Fprintf(w, "Flags trip at: coast >%.2gs, lock >%.2gs, spin >%.2gs, spread >%.0f km/h, corr >=%d.\n",
		coachCoastFlagSeconds, coachLockFlagSeconds, coachSpinFlagSeconds,
		coachExitSDFlagKPH, coachCorrectionsFlag)
	fmt.Fprintln(w, "Grade counts flags (0=A .. 4+=F). It is a triage hint, not a measurement —")
	fmt.Fprintln(w, "the thresholds are heuristics, not a model of a well-driven corner.")
	fmt.Fprintln(w, "Lock/Spin are sample counts at 60 Hz. Braking that starts on the preceding")
	fmt.Fprintln(w, "straight is charged to that straight; see the analyze phase table for it.")

	unmeasured := 0
	for _, r := range rows {
		if r.Laps < 2 {
			unmeasured++
		}
	}
	if unmeasured > 0 {
		fmt.Fprintf(w, "ExitSD is '-' for %d corner(s): fewer than 2 comparable laps, so spread\n", unmeasured)
		fmt.Fprintln(w, "was not measured. Those corners cannot trip the spread flag.")
	}
	fmt.Fprintln(w)
}

// writeCoachSectorLoss attributes the gap between the analysed lap and the
// driver's own best sectors.
//
// Self-vs-self on purpose: every sector in the "Best" column was set during this
// session, so the total is time the driver has already demonstrated rather than
// a simulated ideal. It is coarse — it names a third of the lap, not a corner —
// which is why it sits under the turn table rather than replacing it.
func writeCoachSectorLoss(w io.Writer, res analyzeResult) {
	s := res.Sectors
	if s == nil || res.AnalysedLap == nil || len(s.Best) == 0 {
		return
	}

	var times []*float32
	for _, l := range s.PerLap {
		if l.Lap == res.AnalysedLap.Number {
			times = l.Times
			break
		}
	}
	if len(times) == 0 {
		return
	}

	spans := sectorSpans(res, len(s.Best))

	fmt.Fprintf(w, "Sector loss — lap %d vs your own best sectors this session\n\n",
		res.AnalysedLap.Number)

	coversW := len("Covers")
	for _, c := range spans {
		if len(c) > coversW {
			coversW = len(c)
		}
	}
	fmt.Fprintf(w, " %-6s | %-*s | %8s | %8s | %6s\n",
		"Sector", coversW, "Covers", "This lap", "Best", "Lost")
	fmt.Fprintf(w, "-%s-|-%s-|-%s-|-%s-|-%s\n",
		strings.Repeat("-", 6), strings.Repeat("-", coversW), strings.Repeat("-", 8),
		strings.Repeat("-", 8), strings.Repeat("-", 6))

	var total float32
	for i, best := range s.Best {
		if i >= len(times) || times[i] == nil {
			continue
		}
		lost := *times[i] - best
		total += lost
		covers := ""
		if i < len(spans) {
			covers = spans[i]
		}
		from := ""
		if i < len(s.BestFromLap) {
			from = fmt.Sprintf(" (L%d)", s.BestFromLap[i])
		}
		fmt.Fprintf(w, " %-6s | %-*s | %8.3f | %8.3f | %6.3f%s\n",
			fmt.Sprintf("S%d", i+1), coversW, covers, *times[i], best, lost, from)
	}
	fmt.Fprintf(w, " %-6s | %-*s | %8s | %8s | %6.3f\n", "", coversW, "", "", "", total)
	fmt.Fprintln(w)

	if s.Theoretical != nil {
		fmt.Fprintf(w, "Theoretical best %s — every sector in it was driven this session.\n",
			analysis.FormatLapTime(*s.Theoretical))
		fmt.Fprintln(w)
	}
}

// sectorSpans names the corners each sector covers, e.g. "T4 Honda > T7".
//
// Returns empty strings when the geometry is unavailable (the coach brief trims
// it); the column then renders blank rather than the table being dropped, since
// the timing itself is still the point.
func sectorSpans(res analyzeResult, n int) []string {
	spans := make([]string, n)
	if res.TrackMap == nil || len(res.TrackMap.Segments) == 0 || res.Sectors == nil {
		return spans
	}
	starts := res.Sectors.StartPct
	if len(starts) != n {
		return spans
	}

	for i := 0; i < n; i++ {
		lo := starts[i]
		hi := float32(1)
		if i+1 < n {
			hi = starts[i+1]
		}
		var names []string
		for _, seg := range res.TrackMap.Segments {
			if seg.Kind != trackmap.KindCorner && seg.Kind != trackmap.KindChicane {
				continue
			}
			if seg.EntryPct >= lo && seg.EntryPct < hi {
				names = append(names, seg.Name)
			}
		}
		switch len(names) {
		case 0:
		case 1:
			spans[i] = names[0]
		default:
			spans[i] = names[0] + " > " + names[len(names)-1]
		}
	}
	return spans
}

// writeCoachMostVariable ranks the worst-repeated corners. Kept separate from
// the table because it answers a different question: the table says which corner
// is slow, this says which one the driver has already driven faster.
func writeCoachMostVariable(w io.Writer, res analyzeResult) {
	type item struct {
		seg, phase string
		sd, best   float32
		lap        int
	}
	var items []item
	for _, c := range res.Consistency {
		if c.Laps < 2 {
			continue
		}
		items = append(items, item{c.Segment, c.Phase, c.ExitSpeedSD, c.BestExitKPH, c.BestExitLap})
	}
	if len(items) == 0 {
		return
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].sd > items[j].sd })
	if len(items) > 3 {
		items = items[:3]
	}

	fmt.Fprintln(w, "Least repeatable exits — pace already demonstrated, not yet repeated:")
	for _, it := range items {
		fmt.Fprintf(w, "  %s %s: +/-%.1f km/h spread, best %.1f km/h on lap %d\n",
			it.seg, it.phase, it.sd, it.best, it.lap)
	}
	fmt.Fprintln(w)
}

func coachSeconds(v float32) string {
	if v == 0 {
		return "    -"
	}
	return fmt.Sprintf("%.2fs", v)
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
