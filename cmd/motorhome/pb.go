package main

// The "pb" subcommand: inspecting, comparing and pruning the personal-best
// store. pb.json accumulates a record per car/track combination and is never
// trimmed by the analyze flow, so this is the only place entries can be read
// back in bulk or removed.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/ibt"
	"github.com/rickymw/MotorHome/internal/pb"
)

// RunPB implements the "pb" subcommand. pbPath is the path to pb.json.
func RunPB(args []string, cfg config.Config, pbPath string) {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}

	switch sub {
	case "list":
		runPBList(args, pbPath)
	case "show":
		runPBShow(args, pbPath)
	case "diff":
		runPBDiff(args, cfg, pbPath)
	case "prune":
		runPBPrune(args, pbPath)
	default:
		pbUsage()
		os.Exit(1)
	}
}

func pbUsage() {
	fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] pb <list|show|diff|prune> [flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  pb list                        list every stored personal best")
	fmt.Fprintln(os.Stderr, "  pb show <filter>               full record (setup + phases) for one entry")
	fmt.Fprintln(os.Stderr, "  pb diff [file.ibt]             setup diff: this session vs the stored PB")
	fmt.Fprintln(os.Stderr, "  pb prune -older-than <days>    remove stale entries (dry run without -apply)")
	fmt.Fprintln(os.Stderr, "  pb prune -match <filter>       remove matching entries (dry run without -apply)")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "<filter> is a case-insensitive substring matched against \"car | track\".")
}

// pbDie reports a fatal error for the pb subcommand and exits.
func pbDie(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "pb: "+format+"\n", args...)
	os.Exit(1)
}

// pbEntries returns the entries of pbf sorted by track then car, so listings
// and prune previews are stable between runs (Go map order is not).
func pbEntries(pbf pb.File) []*pb.PersonalBest {
	out := make([]*pb.PersonalBest, 0, len(pbf))
	for _, e := range pbf {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Track != out[j].Track {
			return out[i].Track < out[j].Track
		}
		return out[i].Car < out[j].Car
	})
	return out
}

// matchPBEntries returns the entries whose "car | track" contains filter,
// case-insensitively. An empty filter matches everything.
func matchPBEntries(entries []*pb.PersonalBest, filter string) []*pb.PersonalBest {
	if filter == "" {
		return entries
	}
	needle := strings.ToLower(filter)
	var out []*pb.PersonalBest
	for _, e := range entries {
		hay := strings.ToLower(e.Car + " | " + e.Track)
		if strings.Contains(hay, needle) {
			out = append(out, e)
		}
	}
	return out
}

// loadPBFile loads pb.json for the pb subcommand, failing loudly. Unlike the
// analyze flow — where a missing store is normal on a first run — every pb
// subcommand is about existing records, so an empty store is worth reporting.
func loadPBFile(pbPath string) pb.File {
	if pbPath == "" {
		pbDie("no pb.json path configured")
	}
	pbf, err := pb.Load(pbPath)
	if err != nil {
		pbDie("loading %s: %v", pbPath, err)
	}
	if len(pbf) == 0 {
		pbDie("no PB entries in %s — run analyze on a session with a flying lap first", pbPath)
	}
	return pbf
}

// ---- list ----

func runPBList(args []string, pbPath string) {
	fs := flag.NewFlagSet("pb list", flag.ExitOnError)
	fs.Usage = pbUsage
	_ = fs.Parse(args)

	entries := matchPBEntries(pbEntries(loadPBFile(pbPath)), strings.Join(fs.Args(), " "))
	if len(entries) == 0 {
		pbDie("no entries match %q", strings.Join(fs.Args(), " "))
	}

	carW, trackW := 3, 5
	for _, e := range entries {
		if len(e.Car) > carW {
			carW = len(e.Car)
		}
		if len(e.Track) > trackW {
			trackW = len(e.Track)
		}
	}

	fmt.Printf("  %-*s  %-*s  %10s  %-10s  %s\n", carW, "Car", trackW, "Track", "PB", "Date", "Stored")
	fmt.Printf("  %s  %s  %10s  %-10s  %s\n", dashes(carW), dashes(trackW), dashes(10), dashes(10), dashes(6))
	for _, e := range entries {
		fmt.Printf("  %-*s  %-*s  %10s  %-10s  %s\n",
			carW, e.Car, trackW, e.Track,
			fallback(e.LapTimeFormatted, "—"),
			fallback(e.Date, "—"),
			storedMarkers(e))
	}
	fmt.Println()
	fmt.Printf("  %d %s in %s\n", len(entries), pluralize(len(entries), "entry", "entries"), pbPath)
}

// storedMarkers summarises which optional payloads an entry carries. An entry
// with no phases produces no vs-PB table on the next run, and one with no setup
// cannot be diffed — both are worth seeing at a glance.
func storedMarkers(e *pb.PersonalBest) string {
	var parts []string
	if len(e.Phases) > 0 {
		parts = append(parts, fmt.Sprintf("%d phases", len(e.Phases)))
	}
	if e.Setup != "" {
		parts = append(parts, "setup")
	}
	if len(e.BrakeEntries) > 0 {
		parts = append(parts, fmt.Sprintf("%d brake pts", len(e.BrakeEntries)))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

// ---- show ----

func runPBShow(args []string, pbPath string) {
	fs := flag.NewFlagSet("pb show", flag.ExitOnError)
	fs.Usage = pbUsage
	_ = fs.Parse(args)

	filter := strings.Join(fs.Args(), " ")
	entry := resolveSinglePB(loadPBFile(pbPath), filter)

	// printStoredPB writes through the analyze output sink, which is only bound
	// inside RunAnalyze; point it at stdout for this standalone render.
	analyzeOut = os.Stdout
	printStoredPB(entry)
}

// resolveSinglePB narrows the store to exactly one entry, or exits explaining
// why it could not.
func resolveSinglePB(pbf pb.File, filter string) *pb.PersonalBest {
	entries := pbEntries(pbf)
	matches := matchPBEntries(entries, filter)

	switch {
	case len(matches) == 1:
		return matches[0]
	case len(matches) == 0:
		fmt.Fprintf(os.Stderr, "pb: no entry matches %q. Available:\n", filter)
		for _, e := range entries {
			fmt.Fprintf(os.Stderr, "  %s | %s\n", e.Car, e.Track)
		}
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "pb: %q matches %d entries — narrow it:\n", filter, len(matches))
		for _, e := range matches {
			fmt.Fprintf(os.Stderr, "  %s | %s\n", e.Car, e.Track)
		}
		os.Exit(1)
	}
	return nil
}

// ---- diff ----

// runPBDiff compares the car setup used in a session against the setup stored
// with that car/track's personal best — answering "what have I changed since
// the lap I'm trying to beat?".
func runPBDiff(args []string, cfg config.Config, pbPath string) {
	fs := flag.NewFlagSet("pb diff", flag.ExitOnError)
	showAll := fs.Bool("all", false,
		"include session-state readings (hot pressures, tyre temps, tread) that differ between any two sessions")
	fs.Usage = pbUsage
	_ = fs.Parse(args)

	ibtPath := ""
	switch fs.NArg() {
	case 0:
		if cfg.IbtDir == "" {
			pbDie("pb diff needs an .ibt file (no ibtDir configured)")
		}
		var err error
		ibtPath, err = nthLatestIbtFile(cfg.IbtDir, 1)
		if err != nil {
			pbDie("%v", err)
		}
	case 1:
		ibtPath = fs.Arg(0)
	default:
		pbUsage()
		os.Exit(1)
	}

	f, err := ibt.Open(ibtPath)
	if err != nil {
		pbDie("opening %s: %v", ibtPath, err)
	}
	defer f.Close()

	meta := analysis.ParseSessionMeta(f.SessionInfo(), cfg.Driver)
	pbf := loadPBFile(pbPath)
	entry := pbf[pb.Key(meta.CarScreenName, meta.TrackDisplayName)]
	if entry == nil {
		pbDie("no stored PB for %q on %q", meta.CarScreenName, meta.TrackDisplayName)
	}
	if entry.Setup == "" {
		pbDie("the stored PB for %q on %q has no setup recorded — it predates setup capture,\n"+
			"     or was set in a session whose YAML had no CarSetup block. Set a new PB to populate it.",
			meta.CarScreenName, meta.TrackDisplayName)
	}

	current := analysis.ParseCarSetupTree(f.SessionInfo())
	if current == nil {
		pbDie("no CarSetup block in %s", filepath.Base(ibtPath))
	}

	pbSetup := analysis.FlattenSetup(analysis.ParseCarSetupTree(entry.Setup))
	curSetup := analysis.FlattenSetup(current)
	diff := analysis.DiffSetups(pbSetup, curSetup)
	hidden := 0
	if !*showAll {
		diff, hidden = analysis.FilterSessionState(diff)
	}

	fmt.Printf("Car:   %s\n", fallback(meta.CarScreenName, "(unknown)"))
	fmt.Printf("Track: %s\n", fallback(meta.TrackDisplayName, "(unknown)"))
	fmt.Printf("PB:    %s — set %s\n", entry.LapTimeFormatted, fallback(entry.Date, "?"))
	fmt.Printf("This:  %s\n\n", filepath.Base(ibtPath))

	if len(diff) == 0 {
		fmt.Println("No setup changes since the PB.")
		if hidden > 0 {
			fmt.Printf("(%d session-state %s hidden — pass -all to see them.)\n",
				hidden, pluralize(hidden, "reading", "readings"))
		}
		return
	}

	pathW := 4
	oldW := 3
	for _, d := range diff {
		if len(d.Path) > pathW {
			pathW = len(d.Path)
		}
		if len(d.Old) > oldW {
			oldW = len(d.Old)
		}
	}

	fmt.Printf("  %-*s  %-*s  %s\n", pathW, "Setting", oldW, "PB", "Now")
	fmt.Printf("  %s  %s  %s\n", dashes(pathW), dashes(oldW), dashes(3))
	for _, d := range diff {
		fmt.Printf("  %-*s  %-*s  %s\n",
			pathW, d.Path,
			oldW, fallback(d.Old, "—"),
			fallback(d.New, "—"))
	}
	fmt.Println()
	fmt.Printf("  %d %s differ from the PB setup.\n", len(diff), pluralize(len(diff), "setting", "settings"))
	if hidden > 0 {
		fmt.Printf("  %d session-state %s hidden (hot pressures, tyre temps, tread) — pass -all to see them.\n",
			hidden, pluralize(hidden, "reading", "readings"))
	}
}

// ---- prune ----

// runPBPrune removes entries from pb.json.
//
// It previews by default and only writes with -apply. Deleting a PB record
// throws away accumulated brake-entry positions and the setup that produced the
// lap, none of which can be recovered from telemetry that may itself be long
// deleted — so the destructive path is the one that has to be asked for.
func runPBPrune(args []string, pbPath string) {
	fs := flag.NewFlagSet("pb prune", flag.ExitOnError)
	olderThan := fs.Int("older-than", 0, "remove entries whose PB was set more than this many days ago")
	match := fs.String("match", "", "remove entries matching this case-insensitive substring of \"car | track\"")
	apply := fs.Bool("apply", false, "actually write the change (without this, prune only previews)")
	fs.Usage = pbUsage
	_ = fs.Parse(args)

	if *olderThan <= 0 && *match == "" {
		pbDie("prune needs -older-than <days> or -match <filter> — refusing to select every entry")
	}

	pbf := loadPBFile(pbPath)
	entries := pbEntries(pbf)

	var doomed []*pb.PersonalBest
	var undated []*pb.PersonalBest
	cutoff := time.Now().AddDate(0, 0, -*olderThan)

	for _, e := range entries {
		if *match != "" && len(matchPBEntries([]*pb.PersonalBest{e}, *match)) == 0 {
			continue
		}
		if *olderThan > 0 {
			d, err := time.Parse("2006-01-02", e.Date)
			if err != nil {
				// An entry with no parseable date cannot be shown to be stale.
				// Keeping it is the safe direction, but it must be reported —
				// otherwise a prune that silently skips records looks complete.
				undated = append(undated, e)
				continue
			}
			if !d.Before(cutoff) {
				continue
			}
		}
		doomed = append(doomed, e)
	}

	for _, e := range undated {
		fmt.Fprintf(os.Stderr, "pb: skipping %s | %s — no usable date (%q)\n", e.Car, e.Track, e.Date)
	}

	if len(doomed) == 0 {
		fmt.Println("Nothing to prune.")
		return
	}

	verb := "Would remove"
	if *apply {
		verb = "Removing"
	}
	fmt.Printf("%s %d %s:\n\n", verb, len(doomed), pluralize(len(doomed), "entry", "entries"))
	for _, e := range doomed {
		fmt.Printf("  %s | %s — %s set %s (%s)\n",
			e.Car, e.Track,
			fallback(e.LapTimeFormatted, "—"),
			fallback(e.Date, "?"),
			storedMarkers(e))
	}
	fmt.Println()

	if !*apply {
		fmt.Println("Dry run — nothing was written. Re-run with -apply to remove these.")
		return
	}

	for _, e := range doomed {
		delete(pbf, pb.Key(e.Car, e.Track))
	}
	if err := pb.Save(pbPath, pbf); err != nil {
		pbDie("saving %s: %v", pbPath, err)
	}
	fmt.Printf("Removed %d %s; %d remain.\n",
		len(doomed), pluralize(len(doomed), "entry", "entries"), len(pbf))
}
