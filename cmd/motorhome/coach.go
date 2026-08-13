package main

// The "coach" subcommand: emit a single self-contained coaching brief for an
// AI assistant (Claude Code) to act on.
//
// This exists so coaching is one command rather than a procedure. Previously
// the assistant had to run `analyze`, separately read coach.md, and reconcile
// an ASCII table against a framework written for a human. `coach` bundles the
// orientation, the framework, and a coaching-focused JSON payload into one
// document, so nothing depends on the assistant's working directory or on it
// remembering the extra steps.
//
// It deliberately makes no network call and needs no API key: the assistant
// reading the output *is* the coach.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/config"
)

// coachFrameworkFile is the hand-maintained coaching framework at the repo
// root. It is the single source of truth for how to read the telemetry — the
// brief embeds it rather than restating it, so the two cannot drift.
const coachFrameworkFile = "coach.md"

// RunCoach implements the "coach" subcommand.
//
// It shells out to the same analysis path as `analyze -json` by calling
// RunAnalyze in-process with JSON output captured, then wraps the result.
func RunCoach(args []string, cfg config.Config, trackmapPath, pbPath, notesDir, cfgPath string) {
	fs := flag.NewFlagSet("coach", flag.ExitOnError)
	lapArg := fs.String("lap", "", "lap to coach: integer for that lap, empty for best of session")
	noFramework := fs.Bool("no-framework", false, "omit the embedded coaching framework (data only)")
	table := fs.Bool("table", false, "print a turn-by-turn table for a human instead of the AI brief")
	fuelLaps := fs.Int("fuel-laps", 0, "also report the fuel needed to complete this many laps")
	segment := fs.String("segment", "", "focus on these corners: comma-separated names or 1-based indices (e.g. T3,T4)")
	hz := fs.Int("hz", 0, "sample rate for the traces -segment inlines (default 60)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] coach [flags] [file.ibt]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Emits a self-contained coaching brief (framework + session data) for an")
		fmt.Fprintln(os.Stderr, "AI assistant to act on. Makes no network call and needs no API key.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  motorhome coach                    (most recent session)")
		fmt.Fprintln(os.Stderr, "  motorhome coach -lap 3")
		fmt.Fprintln(os.Stderr, "  motorhome coach -segment T3       (one corner, with its samples)")
		fmt.Fprintln(os.Stderr, "  motorhome coach -segment T3,T4 -hz 20")
		fmt.Fprintln(os.Stderr, "  motorhome coach -table             (turn-by-turn table, for a human)")
		fmt.Fprintln(os.Stderr, "  motorhome coach session.ibt")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if *hz != 0 {
		if *hz < 1 || *hz > analysis.SampleRateHz {
			coachDie("-hz must be between 1 and %d, got %d", analysis.SampleRateHz, *hz)
		}
		if *segment == "" {
			coachDie("-hz sets the rate of the traces -segment inlines; it has no effect on its own")
		}
	}

	// RunAnalyze is about to run in-process, and anything it refuses to do is
	// coach refusing as far as the user is concerned. Without this its errors
	// arrive prefixed "analyze:" and cite flags like -trace that the user never
	// typed.
	invokedAs = coachInvocation

	// Reuse the analyze pipeline wholesale rather than reimplementing it: the
	// brief must describe exactly what `analyze` would report, and a second
	// code path would be free to drift from it.
	analyzeArgs := []string{"-json"}
	if *lapArg != "" {
		analyzeArgs = append(analyzeArgs, "-lap", *lapArg)
	}
	if *fuelLaps > 0 {
		analyzeArgs = append(analyzeArgs, "-fuel-laps", fmt.Sprint(*fuelLaps))
	}
	// -table wants the focused corners but not their samples: a 60Hz CSV is not
	// something a human scans between runs, and the table has no column for it.
	if *segment != "" && !*table {
		analyzeArgs = append(analyzeArgs, "-trace", *segment)
		if *hz > 0 {
			analyzeArgs = append(analyzeArgs, "-hz", fmt.Sprint(*hz))
		}
	}
	analyzeArgs = append(analyzeArgs, fs.Args()...)

	raw, err := captureAnalyzeJSON(analyzeArgs, cfg, trackmapPath, pbPath, notesDir)
	if err != nil {
		coachDie("%v", err)
	}

	var res analyzeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		coachDie("parsing analysis output: %v", err)
	}

	var focus []string
	if *segment != "" {
		var ferr error
		res, focus, ferr = focusOnSegments(res, *segment)
		if ferr != nil {
			coachDie("%v", ferr)
		}
	}

	// -table renders from the untrimmed result: the turn table needs the track
	// map's segment kinds to tell a corner from a straight, which is exactly the
	// geometry trimForCoaching drops for the AI brief.
	if *table {
		fmt.Print(buildCoachTableView(res, focus))
		return
	}

	// Traces travel outside the JSON document. They are already CSV; nesting
	// them in an indent-encoded object would cost tokens and readability for
	// nothing, and as their own fenced blocks they read as what they are.
	traces := res.Traces
	res.Traces = nil

	brief := buildCoachBrief(trimForCoaching(res), coachFrameworkText(*noFramework, cfgPath), focus, traces)
	fmt.Print(brief)
}

// buildCoachTableView renders the human-facing turn-by-turn view: the same
// orientation the brief opens with, then the table.
//
// The orientation is kept because the table is meaningless without it — a D on a
// corner means something different on a 2-lap session with a low-confidence map
// than on a 20-lap one, and the Gaps line is what says which this was.
func buildCoachTableView(res analyzeResult, focus []string) string {
	var b strings.Builder
	writeCoachOrientation(&b, res, focus)
	writeCoachTable(&b, res)
	writeCoachMostVariable(&b, res)
	return b.String()
}

// trimForCoaching drops payload that carries no coaching signal.
//
// Only the track map's raw segment geometry qualifies: entry/exit percentages
// and metre offsets describe where a corner is, which the coach never reasons
// about — every segment is already named in the phase and consistency rows.
// The map's confidence and match score stay, because a low-confidence map
// means the segment boundaries themselves are suspect and findings pinned to
// them should be hedged.
//
// Everything else is left alone. Phases and consistency dominate the payload,
// but they are the substance of the analysis, not overhead.
func trimForCoaching(res analyzeResult) analyzeResult {
	if res.TrackMap != nil {
		trimmed := *res.TrackMap
		trimmed.SegmentCount = len(trimmed.Segments)
		trimmed.Segments = nil
		res.TrackMap = &trimmed
	}
	return res
}

func coachDie(format string, args ...any) {
	fmt.Fprint(os.Stderr, dieMessage(coachInvocation.cmd, format, args...))
	os.Exit(1)
}

// captureAnalyzeJSON runs the analyze pipeline with -json and returns the
// document. RunAnalyze writes JSON to os.Stdout, so stdout is swapped for a
// pipe around the call — the same mechanism main.go uses for the clipboard.
func captureAnalyzeJSON(args []string, cfg config.Config, trackmapPath, pbPath, notesDir string) ([]byte, error) {
	finish, err := captureStdoutSilent()
	if err != nil {
		return nil, fmt.Errorf("capturing analysis output: %w", err)
	}
	RunAnalyze(args, cfg, trackmapPath, pbPath, notesDir)
	out := finish()
	if strings.TrimSpace(out) == "" {
		return nil, fmt.Errorf("analysis produced no output")
	}
	return []byte(out), nil
}

// coachFrameworkText loads coach.md from the config directory (where the
// binary and its runtime files live), falling back to the current directory.
//
// A missing framework is not fatal: the data section still carries everything
// measured, and the assistant can coach from it. Silently emitting a brief
// that claims to include a framework it doesn't would be worse.
func coachFrameworkText(omit bool, cfgPath string) string {
	if omit {
		return ""
	}
	candidates := []string{
		filepath.Join(filepath.Dir(cfgPath), coachFrameworkFile),
		coachFrameworkFile,
	}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	fmt.Fprintf(os.Stderr,
		"coach: warning: %s not found — the brief carries data but no coaching framework\n",
		coachFrameworkFile)
	return ""
}

// buildCoachBrief renders the markdown brief.
func buildCoachBrief(res analyzeResult, framework string, focus []string,
	traces []analysis.SegmentTrace) string {

	var b strings.Builder
	writeCoachBrief(&b, res, framework, focus, traces)
	return b.String()
}

func writeCoachBrief(w io.Writer, res analyzeResult, framework string, focus []string,
	traces []analysis.SegmentTrace) {
	fmt.Fprintf(w, "# Coaching brief — %s at %s\n\n",
		fallback(res.Car, "(unknown car)"), fallback(res.Track, "(unknown track)"))

	fmt.Fprintln(w, "You are the race engineer. Read the framework, then the session data below,")
	fmt.Fprintln(w, "and deliver per-segment findings followed by a **Top 3 Actions** list.")
	fmt.Fprintln(w)

	writeCoachOrientation(w, res, focus)

	if framework != "" {
		fmt.Fprintln(w, "---")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "# Framework")
		fmt.Fprintln(w)
		fmt.Fprintln(w, strings.TrimRight(framework, "\n"))
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# Session data")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Schema `%s`. Speeds km/h, brake/throttle percent, coast seconds, fuel litres.\n",
		fallback(res.Schema, analyzeSchema))
	fmt.Fprintln(w, "Deltas under `vsPB` are current minus personal best — positive speed is faster.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	// The document was just unmarshalled from valid JSON, so re-encoding it
	// cannot fail on content; ignore the error rather than adding a path that
	// can never run.
	_ = enc.Encode(res)
	fmt.Fprintln(w, "```")

	writeCoachTraces(w, traces)
}

// writeCoachTraces appends the sample-level traces as fenced CSV blocks.
//
// These are the reason -segment exists. The aggregate rows above say a corner is
// slow and inconsistent; these say on which lap, at which point in the corner,
// and by how much — the difference between "your T3 exit varies" and "on lap 4
// you were still at 12% throttle 0.4s after the apex you took at 60% on lap 3".
func writeCoachTraces(w io.Writer, traces []analysis.SegmentTrace) {
	if len(traces) == 0 {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# Sample-level traces")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "One block per focused corner, every comparable lap overlaid. Compare the laps")
	fmt.Fprintln(w, "against each other at equal `Time`, and against the phase rows above.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Columns: `Lap`; `Dist%` position round the lap (0–1); `Time` seconds from the")
	fmt.Fprintln(w, "start of *that lap's* window, restarting at 0 for each lap so the traces")
	fmt.Fprintln(w, "overlay; `Speed` km/h; `Throttle`/`Brake` 0–100; `Steer` degrees at the wheel")
	fmt.Fprintln(w, "(+ = left, not road-wheel angle); `Gear`; `LatG`/`LongG` in g; `ABS` and")
	fmt.Fprintln(w, "`Coast` 0/1. Each block includes ~1s of lead-in and lead-out beyond the")
	fmt.Fprintln(w, "segment boundary, so braking before the corner is visible.")
	fmt.Fprintln(w)

	for _, tr := range traces {
		fmt.Fprintf(w, "## %s (%s) — laps %s at %dHz\n\n",
			tr.Segment, tr.Kind, intsJoin(tr.Laps), tr.RateHz)
		if tr.RateHz < analysis.SampleRateHz {
			fmt.Fprintf(w,
				"Downsampled from %dHz. `ABS` and `Coast` are 1 if set anywhere in the window a\n"+
					"row covers, so brief events survive; every other column is a point sample.\n\n",
				analysis.SampleRateHz)
		}
		fmt.Fprintln(w, "```csv")
		fmt.Fprintln(w, tr.Columns)
		for _, row := range tr.Rows {
			fmt.Fprintln(w, row)
		}
		fmt.Fprintln(w, "```")
		fmt.Fprintln(w)
	}
}

// writeCoachOrientation summarises the session in prose, so the reader knows
// what kind of session it was before reading a single number — a 3-lap
// practice run and a 40-lap race warrant different coaching.
func writeCoachOrientation(w io.Writer, res analyzeResult, focus []string) {
	fmt.Fprintln(w, "## Session")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- File: %s\n", fallback(res.File, "(unknown)"))
	if res.SessionDate != "" {
		fmt.Fprintf(w, "- Date: %s\n", res.SessionDate)
	}
	if res.Driver != "" {
		fmt.Fprintf(w, "- Driver: %s\n", res.Driver)
	}

	total, flying, comparable, cut := coachLapCounts(res)
	fmt.Fprintf(w, "- Laps: %d total, %d flying, %d comparable", total, flying, comparable)
	if cut > 0 {
		fmt.Fprintf(w, ", %d cut", cut)
	}
	fmt.Fprintln(w)

	if res.AnalysedLap != nil {
		fmt.Fprintf(w, "- Analysed: lap %d (%s, selected as %s)\n",
			res.AnalysedLap.Number, res.AnalysedLap.TimeFormatted, res.AnalysedLap.Selection)
	}

	if res.PB != nil {
		if res.PB.DeltaToBest < 0 {
			fmt.Fprintf(w, "- PB: %s — this session beat it by %.3fs\n",
				res.PB.LapTimeFormatted, -res.PB.DeltaToBest)
		} else {
			fmt.Fprintf(w, "- PB: %s set %s — this session is %.3fs off it\n",
				res.PB.LapTimeFormatted, fallback(res.PB.Date, "?"), res.PB.DeltaToBest)
		}
	}

	// Call out what is absent, so the reader doesn't coach around a gap
	// without realising there is one.
	var missing []string
	if res.TrackMap == nil {
		missing = append(missing, "no track map (segments unnamed; zone fallback only)")
	}
	if len(res.Consistency) == 0 {
		missing = append(missing, "no consistency data (needs 2+ comparable laps)")
	}
	if res.PB == nil {
		missing = append(missing, "no stored PB to compare against")
	}
	if res.Sectors == nil {
		missing = append(missing, "no sector times published for this session type")
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "- Gaps: %s\n", strings.Join(missing, "; "))
	}

	if len(res.Notes) > 0 {
		fmt.Fprintf(w, "- Voice notes: %d (the driver's own words — weigh them heavily)\n", len(res.Notes))
	}

	// Stated last so it is the thing read immediately before the data, and
	// stated at all because a narrowed brief is otherwise indistinguishable
	// from a whole-session one. Findings about corners that were removed cannot
	// be made; "the rest of the lap is fine" is not something this document
	// supports.
	if len(focus) > 0 {
		fmt.Fprintf(w, "- **Focus: %s.** Per-segment rows for every other corner were removed\n",
			strings.Join(focus, ", "))
		fmt.Fprintln(w, "  before this was written. Coach only what is here, and do not")
		fmt.Fprintln(w, "  characterise the lap as a whole or rank these corners against the rest.")
	}
	fmt.Fprintln(w)
}

// coachLapCounts summarises the lap population for the orientation section.
func coachLapCounts(res analyzeResult) (total, flying, comparable, cut int) {
	for _, l := range res.Laps {
		total++
		if l.Kind == "flying lap" {
			flying++
		}
		if l.Comparable {
			comparable++
		}
		if l.Cut {
			cut++
		}
	}
	return total, flying, comparable, cut
}
