package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

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
	updateMap := fs.Bool("update-map", false, "ignore existing track map and re-detect from this session")
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

	sessionID := f.DiskHeader().SessionStartDate.UTC().Format(time.RFC3339)

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

	var geomConf trackmap.GeometryConfidence
	var matchScore float32 = -1 // -1 means "not computed" (no stored map yet)

	existingTM, hasExisting := tmf[meta.TrackDisplayName]
	useExisting := hasExisting && len(existingTM.Segments) > 0 && !*updateMap

	if useExisting {
		segs = existingTM.Segments
		geomConf = existingTM.Confidence()

		// Compute match score from best lap.
		if bestLap != nil && trackLengthM > 0 {
			tsamples := make([]trackmap.Sample, len(bestLap.Samples))
			for i, s := range bestLap.Samples {
				tsamples[i] = trackmap.Sample{LapDistPct: s.LapDistPct, LatAccel: s.LatAccel}
			}
			matchScore = trackmap.MatchScore(tsamples, segs)
		}

		// Increment usage counters and re-save, but only once per session.
		if trackmapPath != "" && !existingTM.HasSession(sessionID) {
			flyingCount := 0
			for _, l := range laps {
				if l.Kind == analysis.KindFlying && !l.IsPartialStart {
					flyingCount++
				}
			}
			existingTM.LapsUsed += flyingCount
			existingTM.SessionsUsed++
			existingTM.AddSession(sessionID)
			_ = trackmap.Save(trackmapPath, tmf)
		}
	} else if trackLengthM > 0 && bestLap != nil {
		// Auto-detect from all flying, non-partial-start laps for more stable boundaries.
		var allSamples [][]trackmap.Sample
		for i := range laps {
			l := &laps[i]
			if l.Kind != analysis.KindFlying || l.IsPartialStart {
				continue
			}
			ts := make([]trackmap.Sample, len(l.Samples))
			for j, s := range l.Samples {
				ts[j] = trackmap.Sample{LapDistPct: s.LapDistPct, LatAccel: s.LatAccel}
			}
			allSamples = append(allSamples, ts)
		}
		if len(allSamples) == 0 {
			// Fallback: use bestLap only (e.g. all laps are partial-start).
			ts := make([]trackmap.Sample, len(bestLap.Samples))
			for i, s := range bestLap.Samples {
				ts[i] = trackmap.Sample{LapDistPct: s.LapDistPct, LatAccel: s.LatAccel}
			}
			allSamples = [][]trackmap.Sample{ts}
		}
		segs = trackmap.DetectFromMultiple(allSamples, trackLengthM)
		if trackmapPath != "" && len(segs) > 0 {
			newTM := &trackmap.TrackMap{
				TrackLengthM: trackLengthM,
				Source:       "auto",
				DetectedFrom: trackmap.Today(),
				LapsUsed:     len(allSamples),
				SessionsUsed: 1,
				Segments:     segs,
			}
			newTM.AddSession(sessionID)
			tmf[meta.TrackDisplayName] = newTM
			_ = trackmap.Save(trackmapPath, tmf)
			if *updateMap {
				fmt.Printf("Track map updated: %d segments detected for %s\n\n",
					len(segs), meta.TrackDisplayName)
			} else {
				fmt.Printf("Track map created: %d segments detected for %s\n\n",
					len(segs), meta.TrackDisplayName)
			}
		}
	}

	// Print map confidence line.
	if len(segs) > 0 {
		if matchScore >= 0 {
			// Loaded from existing map.
			lapWord := "lap"
			if existingTM.LapsUsed != 1 {
				lapWord = "laps"
			}
			sessionWord := "session"
			if existingTM.SessionsUsed != 1 {
				sessionWord = "sessions"
			}
			fmt.Printf("Map:     %d segs — geometry: %s (%d %s, %d %s) — match: %.0f%%\n\n",
				len(segs), geomConf,
				existingTM.LapsUsed, lapWord,
				existingTM.SessionsUsed, sessionWord,
				matchScore*100)
		} else {
			// Just detected for the first time this session.
			// len(allSamples) is not in scope here; the new TrackMap was saved with
			// the correct LapsUsed but segs came back without a reference to tmf entry.
			// Use the newly written entry if available.
			detectedLaps := 1
			if newTM, ok := tmf[meta.TrackDisplayName]; ok {
				detectedLaps = newTM.LapsUsed
			}
			lapWord := "lap"
			if detectedLaps != 1 {
				lapWord = "laps"
			}
			fmt.Printf("Map:     %d segs — geometry: low (%d %s, 1 session) — match: n/a (first detection)\n\n",
				len(segs), detectedLaps, lapWord)
		}

		// Low match score warning.
		if matchScore >= 0 && matchScore < 0.70 {
			fmt.Printf("Warning: lap profile matches stored map at only %.0f%% — consider running with\n", matchScore*100)
			fmt.Println("         -update-map to regenerate segment boundaries from this session.")
			fmt.Println()
		}
	}

	fmt.Println("Laps:")
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
