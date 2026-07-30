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
	fuelLaps := fs.Int("fuel-laps", 0, "also report the fuel needed to complete this many laps")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] coach [flags] [file.ibt]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Emits a self-contained coaching brief (framework + session data) for an")
		fmt.Fprintln(os.Stderr, "AI assistant to act on. Makes no network call and needs no API key.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  motorhome coach                    (most recent session)")
		fmt.Fprintln(os.Stderr, "  motorhome coach -lap 3")
		fmt.Fprintln(os.Stderr, "  motorhome coach session.ibt")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

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
	analyzeArgs = append(analyzeArgs, fs.Args()...)

	raw, err := captureAnalyzeJSON(analyzeArgs, cfg, trackmapPath, pbPath, notesDir)
	if err != nil {
		coachDie("%v", err)
	}

	var res analyzeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		coachDie("parsing analysis output: %v", err)
	}

	brief := buildCoachBrief(trimForCoaching(res), coachFrameworkText(*noFramework, cfgPath))
	fmt.Print(brief)
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
	fmt.Fprintf(os.Stderr, "coach: "+format+"\n", args...)
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
func buildCoachBrief(res analyzeResult, framework string) string {
	var b strings.Builder
	writeCoachBrief(&b, res, framework)
	return b.String()
}

func writeCoachBrief(w io.Writer, res analyzeResult, framework string) {
	fmt.Fprintf(w, "# Coaching brief — %s at %s\n\n",
		fallback(res.Car, "(unknown car)"), fallback(res.Track, "(unknown track)"))

	fmt.Fprintln(w, "You are the race engineer. Read the framework, then the session data below,")
	fmt.Fprintln(w, "and deliver per-segment findings followed by a **Top 3 Actions** list.")
	fmt.Fprintln(w)

	writeCoachOrientation(w, res)

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
}

// writeCoachOrientation summarises the session in prose, so the reader knows
// what kind of session it was before reading a single number — a 3-lap
// practice run and a 40-lap race warrant different coaching.
func writeCoachOrientation(w io.Writer, res analyzeResult) {
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
