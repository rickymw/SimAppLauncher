package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rickymw/SimAppLauncher/internal/analysis"
	"github.com/rickymw/SimAppLauncher/internal/config"
	"github.com/rickymw/SimAppLauncher/internal/ibt"
	"github.com/rickymw/SimAppLauncher/internal/trackmap"
)

// RunAnalyze implements the "analyze" subcommand.
// args contains everything after "analyze" on the command line.
// trackmapPath is the path to trackmap.json; "" disables load/save.
func RunAnalyze(args []string, cfg config.Config, trackmapPath string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	lapNum := fs.Int("lap", 0, "lap number to analyze (0 = best completed lap)")
	compare := fs.String("compare", "", "compare two laps, e.g. -compare 1,2")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: simapplauncher [-config <path>] analyze [flags] <file.ibt>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  simapplauncher analyze session.ibt")
		fmt.Fprintln(os.Stderr, "  simapplauncher analyze -lap 2 session.ibt")
		fmt.Fprintln(os.Stderr, "  simapplauncher analyze -compare 1,2 session.ibt")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(1)
	}

	f, err := ibt.Open(fs.Arg(0))
	if err != nil {
		analyzeDie("opening file: %v", err)
	}
	defer f.Close()

	meta := analysis.ParseSessionMeta(f.SessionInfo(), cfg.Driver)
	fmt.Printf("Driver:  %s\n", fallback(meta.DriverName, "(unknown)"))
	fmt.Printf("Car:     %s\n", fallback(meta.CarScreenName, "(unknown)"))
	fmt.Printf("Track:   %s\n", fallback(meta.TrackDisplayName, "(unknown)"))
	fmt.Printf("Samples: %d at %d Hz\n", f.NumSamples(), f.Header().TickRate)

	laps, err := analysis.ExtractLaps(f)
	if err != nil {
		analyzeDie("extracting laps: %v", err)
	}
	if len(laps) == 0 {
		analyzeDie("no samples found in file")
	}

	fmt.Println("\nLaps:")
	for _, l := range laps {
		if l.LapTime > 0 {
			fmt.Printf("  Lap %2d: %s (%d samples) [%s]\n",
				l.Number, analysis.FormatLapTime(l.LapTime), len(l.Samples), l.Kind)
		} else {
			fmt.Printf("  Lap %2d: incomplete (%d samples) [%s]\n",
				l.Number, len(l.Samples), l.Kind)
		}
	}
	fmt.Println()

	// Resolve the best lap now (needed for auto-detection even when not yet printing).
	bestLap := bestAnalyzeLap(laps)

	// Load or detect track segments.
	trackLengthM := analysis.ParseTrackLength(f.SessionInfo())
	var segs []trackmap.Segment

	var tmf trackmap.TrackMapFile
	if trackmapPath != "" {
		tmf, _ = trackmap.Load(trackmapPath)
	}
	if tmf == nil {
		tmf = trackmap.TrackMapFile{}
	}

	if tm, ok := tmf[meta.TrackDisplayName]; ok && len(tm.Segments) > 0 {
		segs = tm.Segments
	} else if trackLengthM > 0 && bestLap != nil {
		// Auto-detect from best flying lap.
		tsamples := make([]trackmap.Sample, len(bestLap.Samples))
		for i, s := range bestLap.Samples {
			tsamples[i] = trackmap.Sample{LapDistPct: s.LapDistPct, LatAccel: s.LatAccel}
		}
		segs = trackmap.Detect(tsamples, trackLengthM)
		if trackmapPath != "" && len(segs) > 0 {
			tmf[meta.TrackDisplayName] = &trackmap.TrackMap{
				TrackLengthM: trackLengthM,
				Source:       "auto",
				DetectedFrom: trackmap.Today(),
				LapsUsed:     1,
				Segments:     segs,
			}
			_ = trackmap.Save(trackmapPath, tmf)
			fmt.Printf("Track map created: %d segments detected for %s\n\n",
				len(segs), meta.TrackDisplayName)
		}
	}

	if *compare != "" {
		analyzeCompareLaps(laps, *compare, segs)
		return
	}
	analyzeSingleLap(laps, *lapNum, segs)
}

// ---- single lap ----

func analyzeSingleLap(laps []analysis.Lap, lapNum int, segs []trackmap.Segment) {
	var lap *analysis.Lap
	if lapNum > 0 {
		lap = findAnalyzeLap(laps, lapNum)
		if lap == nil {
			analyzeDie("lap %d not found in file", lapNum)
		}
		if lap.Kind != analysis.KindFlying {
			fmt.Printf("Note: Lap %d is a %s — data includes pit lane or standing start.\n\n",
				lap.Number, lap.Kind)
		}
		if lap.IsPartialStart {
			fmt.Printf("Note: Lap %d started mid-recording — lap time is underestimated.\n\n",
				lap.Number)
		}
	} else {
		lap = bestAnalyzeLap(laps)
		if lap == nil {
			analyzeDie("no flying laps found in file (all laps are out laps or in laps)")
		}
		fmt.Printf("Selecting best lap: Lap %d (%s)\n\n",
			lap.Number, analysis.FormatLapTime(lap.LapTime))
	}

	if segs != nil {
		printSegmentTable(lap, analysis.SegmentStats(lap, segs))
	} else {
		printZoneTable(lap, analysis.ZoneStats(lap))
	}
}

// ---- comparison ----

func analyzeCompareLaps(laps []analysis.Lap, arg string, segs []trackmap.Segment) {
	parts := strings.SplitN(arg, ",", 2)
	if len(parts) != 2 {
		analyzeDie("-compare requires two lap numbers separated by a comma, e.g. -compare 1,2")
	}
	n1, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	n2, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if e1 != nil || e2 != nil {
		analyzeDie("-compare: invalid lap numbers %q", arg)
	}
	lap1 := findAnalyzeLap(laps, n1)
	lap2 := findAnalyzeLap(laps, n2)
	if lap1 == nil {
		analyzeDie("lap %d not found in file", n1)
	}
	if lap2 == nil {
		analyzeDie("lap %d not found in file", n2)
	}
	for _, l := range []*analysis.Lap{lap1, lap2} {
		if l.Kind != analysis.KindFlying {
			fmt.Printf("Note: Lap %d is a %s.\n", l.Number, l.Kind)
		}
	}

	if segs != nil {
		zones1 := analysis.SegmentStats(lap1, segs)
		zones2 := analysis.SegmentStats(lap2, segs)
		deltas := analysis.SegmentDeltas(lap1, lap2, segs)
		printSegmentComparisonTable(lap1, lap2, zones1, zones2, deltas)
	} else {
		printComparisonTable(lap1, lap2,
			analysis.ZoneStats(lap1), analysis.ZoneStats(lap2),
			analysis.ZoneDeltas(lap1, lap2))
	}
}

// ---- output ----

func printZoneTable(lap *analysis.Lap, zones []analysis.Zone) {
	fmt.Printf("Lap %d — %s\n\n", lap.Number, analysis.FormatLapTime(lap.LapTime))
	fmt.Println(" Zone | Dist  | EntSpd | MinSpd | ExtSpd | Gear | Brake | Thr  | LatG | ABS | Coast")
	fmt.Println("------|-------|--------|--------|--------|------|-------|------|------|-----|------")
	for _, z := range zones {
		if z.SampleCount == 0 {
			fmt.Printf("  %2d  | %3d%%  |    --- |    --- |    --- |   -- |    -- |   -- |   -- |  -- |   ---\n",
				z.Index+1, (z.Index+1)*5)
			continue
		}
		fmt.Printf("  %2d  | %3d%%  | %6.1f | %6.1f | %6.1f |  %3d | %5.0f%% | %4.0f%% | %4.2f | %3d | %5d\n",
			z.Index+1, (z.Index+1)*5,
			z.SpeedEntryKPH, z.SpeedMinKPH, z.SpeedExitKPH,
			z.DominantGear,
			z.BrakePct, z.ThrottlePct,
			z.LatGMax,
			z.ABSCount, z.CoastSamples)
	}
	fmt.Println()
}

func printComparisonTable(lap1, lap2 *analysis.Lap, zones1, zones2 []analysis.Zone, deltas []float32) {
	fmt.Printf("Lap A: Lap %d (%s)  ↔  Lap B: Lap %d (%s)\n",
		lap1.Number, analysis.FormatLapTime(lap1.LapTime),
		lap2.Number, analysis.FormatLapTime(lap2.LapTime))
	fmt.Printf("Overall delta (B−A): %+.3fs\n\n", lap2.LapTime-lap1.LapTime)
	fmt.Println(" Zone | Dist  | A.Min | B.Min | A.Brk | B.Brk | A.Thr | B.Thr | A.ABS | B.ABS |   Δ(s)")
	fmt.Println("------|-------|-------|-------|-------|-------|-------|-------|-------|-------|-------")
	for i, z1 := range zones1 {
		z2 := zones2[i]
		d := float32(0)
		if i < len(deltas) {
			d = deltas[i]
		}
		fmt.Printf("  %2d  | %3d%%  | %5.1f | %5.1f | %5.0f%% | %5.0f%% | %5.0f%% | %5.0f%% | %5d | %5d | %+6.3f\n",
			i+1, (i+1)*5,
			z1.SpeedMinKPH, z2.SpeedMinKPH,
			z1.BrakePct, z2.BrakePct,
			z1.ThrottlePct, z2.ThrottlePct,
			z1.ABSCount, z2.ABSCount, d)
	}
	fmt.Println()
}

// printSegmentTable prints a single-lap zone table using geometry-based segments.
//
// Example output:
//
//	Lap 5 — 2:11.367
//
//	 Seg  | Name         |  Entry →   Exit | EntSpd | MinSpd | ExtSpd | Gear | Brake |  Thr  | LatG | ABS | Coast
//	------|--------------|-----------------|--------|--------|--------|------|-------|-------|------|-----|------
//	   1  | S1           |  0.0% →   3.2%  |  202.7 |  202.7 |  241.3 |    5 |    0% |  100% | 0.31 |   0 |     0
func printSegmentTable(lap *analysis.Lap, zones []analysis.SegZone) {
	fmt.Printf("Lap %d — %s\n\n", lap.Number, analysis.FormatLapTime(lap.LapTime))
	fmt.Println(" Seg  | Name         |  Entry →   Exit | EntSpd | MinSpd | ExtSpd | Gear | Brake |  Thr  | LatG | ABS | Coast")
	fmt.Println("------|--------------|-----------------|--------|--------|--------|------|-------|-------|------|-----|------")
	for i, z := range zones {
		if z.SampleCount == 0 {
			fmt.Printf("  %2d  | %-12s | %5.1f%% → %5.1f%%  |    --- |    --- |    --- |   -- |    -- |    -- |   -- |  -- |   ---\n",
				i+1, z.Name, z.EntryPct*100, z.ExitPct*100)
			continue
		}
		fmt.Printf("  %2d  | %-12s | %5.1f%% → %5.1f%%  | %6.1f | %6.1f | %6.1f |  %3d | %5.0f%% | %5.0f%% | %4.2f | %3d | %5d\n",
			i+1, z.Name, z.EntryPct*100, z.ExitPct*100,
			z.SpeedEntryKPH, z.SpeedMinKPH, z.SpeedExitKPH,
			z.DominantGear,
			z.BrakePct, z.ThrottlePct,
			z.LatGMax,
			z.ABSCount, z.CoastSamples)
	}
	fmt.Println()
}

// printSegmentComparisonTable prints a side-by-side lap comparison using geometry-based segments.
func printSegmentComparisonTable(lap1, lap2 *analysis.Lap, zones1, zones2 []analysis.SegZone, deltas []float32) {
	fmt.Printf("Lap A: Lap %d (%s)  ↔  Lap B: Lap %d (%s)\n",
		lap1.Number, analysis.FormatLapTime(lap1.LapTime),
		lap2.Number, analysis.FormatLapTime(lap2.LapTime))
	fmt.Printf("Overall delta (B−A): %+.3fs\n\n", lap2.LapTime-lap1.LapTime)
	fmt.Println(" Seg  | Name         |  Entry →   Exit | A.Min | B.Min | A.Brk | B.Brk | A.Thr | B.Thr | A.ABS | B.ABS |   Δ(s)")
	fmt.Println("------|--------------|-----------------|-------|-------|-------|-------|-------|-------|-------|-------|-------")
	for i, z1 := range zones1 {
		var z2 analysis.SegZone
		if i < len(zones2) {
			z2 = zones2[i]
		}
		d := float32(0)
		if i < len(deltas) {
			d = deltas[i]
		}
		fmt.Printf("  %2d  | %-12s | %5.1f%% → %5.1f%%  | %5.1f | %5.1f | %5.0f%% | %5.0f%% | %5.0f%% | %5.0f%% | %5d | %5d | %+6.3f\n",
			i+1, z1.Name, z1.EntryPct*100, z1.ExitPct*100,
			z1.SpeedMinKPH, z2.SpeedMinKPH,
			z1.BrakePct, z2.BrakePct,
			z1.ThrottlePct, z2.ThrottlePct,
			z1.ABSCount, z2.ABSCount, d)
	}
	fmt.Println()
}

// ---- helpers ----

func findAnalyzeLap(laps []analysis.Lap, number int) *analysis.Lap {
	for i := range laps {
		if laps[i].Number == number {
			return &laps[i]
		}
	}
	return nil
}

func bestAnalyzeLap(laps []analysis.Lap) *analysis.Lap {
	var best *analysis.Lap
	for i := range laps {
		l := &laps[i]
		if l.Kind != analysis.KindFlying || l.IsPartialStart {
			continue
		}
		if len(l.Samples) < analysis.MinSamplesForValidLap || l.LapTime <= 0 {
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
