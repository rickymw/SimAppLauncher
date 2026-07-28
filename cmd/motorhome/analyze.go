package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/ibt"
	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// RunAnalyze implements the "analyze" subcommand.
// args contains everything after "analyze" on the command line.
// trackmapPath is the path to trackmap.json; "" disables load/save.
// pbPath is the path to pb.json; "" disables load/save.
func RunAnalyze(args []string, cfg config.Config, trackmapPath, pbPath string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	lapArg := fs.String("lap", "", "lap to analyze: integer for that lap, \"pb\" for stored PB, empty for best of session")
	updateMap := fs.Bool("update-map", false, "ignore existing track map and re-detect from this session")
	dumpSeg := fs.String("dump", "", "dump segment telemetry to CSV (name like T3 or 1-based index)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] analyze [flags] <file.ibt>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  motorhome analyze session.ibt")
		fmt.Fprintln(os.Stderr, "  motorhome analyze -lap 2 session.ibt")
		fmt.Fprintln(os.Stderr, "  motorhome analyze -lap pb            (show stored PB lap)")
		fmt.Fprintln(os.Stderr, "  motorhome analyze -dump T3 session.ibt")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	// Parse -lap: "" → best, "pb" → PB lap from pb.json, integer → that lap number.
	lapMode, lapNum, err := parseLapArg(*lapArg)
	if err != nil {
		analyzeDie("%v", err)
	}

	var ibtPath string
	switch fs.NArg() {
	case 0:
		if cfg.IbtDir == "" {
			// "-lap pb" with no ibtDir falls back to a pure pb.json lookup —
			// no car/track context, so use the only entry or list.
			if lapMode == lapModePB {
				runStoredPBNoIBT(pbPath)
				return
			}
			fs.Usage()
			os.Exit(1)
		}
		var err error
		ibtPath, err = nthLatestIbtFile(cfg.IbtDir, 1)
		if err != nil {
			// Same fallback when ibtDir exists but is empty.
			if lapMode == lapModePB {
				runStoredPBNoIBT(pbPath)
				return
			}
			analyzeDie("%v", err)
		}
		fmt.Printf("File:    %s\n", filepath.Base(ibtPath))
	case 1:
		arg := fs.Arg(0)
		if n, err := strconv.Atoi(arg); err == nil {
			// Numeric argument: treat as 1-based recency index into ibtDir.
			if cfg.IbtDir == "" {
				analyzeDie("numeric argument %d requires ibtDir to be set in config", n)
			}
			if n < 1 {
				analyzeDie("file index must be >= 1, got %d", n)
			}
			var ferr error
			ibtPath, ferr = nthLatestIbtFile(cfg.IbtDir, n)
			if ferr != nil {
				analyzeDie("%v", ferr)
			}
			fmt.Printf("File:    %s\n", filepath.Base(ibtPath))
		} else {
			ibtPath = arg
		}
	default:
		fs.Usage()
		os.Exit(1)
	}

	f, err := ibt.Open(ibtPath)
	if err != nil {
		analyzeDie("opening file: %v", err)
	}
	defer f.Close()

	sessionID := f.DiskHeader().SessionStartDate.UTC().Format(time.RFC3339)

	meta := analysis.ParseSessionMeta(f.SessionInfo(), cfg.Driver)

	// "-lap pb" with an .ibt: use the .ibt only to resolve car/track, then
	// render the stored PB and exit before the normal analysis flow.
	if lapMode == lapModePB {
		runStoredPBForCarTrack(pbPath, meta.CarScreenName, meta.TrackDisplayName)
		return
	}

	fmt.Printf("Driver:  %s\n", fallback(meta.DriverName, "(unknown)"))
	fmt.Printf("Car:     %s\n", fallback(meta.CarScreenName, "(unknown)"))
	fmt.Printf("Track:   %s\n", fallback(meta.TrackDisplayName, "(unknown)"))
	fmt.Printf("Samples: %d at %d Hz\n\n", f.NumSamples(), f.Header().TickRate)

	if nodes := analysis.ParseCarSetupTree(f.SessionInfo()); nodes != nil {
		printSetupTables(nodes)
	}

	laps, err := analysis.ExtractLaps(f)
	if err != nil {
		analyzeDie("extracting laps: %v", err)
	}
	if len(laps) == 0 {
		analyzeDie("no samples found in file")
	}

	// Resolve the best lap now (needed for auto-detection even when not yet printing).
	bestLap := bestAnalyzeLap(laps)

	if bestLap != nil {
		printTyreSummary(bestLap)
	}

	// Load or detect track segments.
	trackLengthM := analysis.ParseTrackLength(f.SessionInfo())
	var segs []trackmap.Segment

	var tmf trackmap.TrackMapFile
	var trf trackmap.TrackRefFile
	if trackmapPath != "" {
		var loadErr error
		tmf, loadErr = trackmap.Load(trackmapPath)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load trackmap.json: %v\n", loadErr)
			tmf = trackmap.TrackMapFile{}
		}
		// Load track reference from the same directory.
		refPath := filepath.Join(filepath.Dir(trackmapPath), "trackref.json")
		trf, loadErr = trackmap.LoadTrackRef(refPath)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load trackref.json: %v\n", loadErr)
			trf = trackmap.TrackRefFile{}
		}
	} else {
		tmf = trackmap.TrackMapFile{}
		trf = trackmap.TrackRefFile{}
	}

	var geomConf trackmap.GeometryConfidence
	var matchScore float32 = -1 // -1 means "not computed" (no stored map yet)

	existingTM, hasExisting := tmf[meta.TrackDisplayName]
	useExisting := hasExisting && len(existingTM.Segments) > 0 && !*updateMap

	// Load pb.json early — used for both brake entries and PB tracking.
	var pbf pb.File
	if pbPath != "" {
		var pbErr error
		pbf, pbErr = pb.Load(pbPath)
		if pbErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load pb.json: %v\n", pbErr)
			pbf = pb.File{}
		}
	} else {
		pbf = pb.File{}
	}

	if useExisting {
		segs = existingTM.Segments

		// Compute match score from best lap using GPS curvature.
		if bestLap != nil && trackLengthM > 0 {
			tsamples := make([]trackmap.Sample, len(bestLap.Samples))
			for i, s := range bestLap.Samples {
				tsamples[i] = trackmap.Sample{LapDistPct: s.LapDistPct, Lat: s.Lat, Lon: s.Lon}
			}
			matchScore = trackmap.MatchScore(tsamples, segs, trackLengthM)
		}

		// Effective confidence is the lower of geometry confidence and match confidence.
		if matchScore >= 0 {
			geomConf = existingTM.EffectiveConfidence(matchScore)
		} else {
			geomConf = existingTM.Confidence()
		}

		// Update brake entries when this is a new session.
		isNewSession := !existingTM.HasSession(sessionID)

		if isNewSession {
			var goodLaps []analysis.Lap
			if bestLap != nil {
				goodLaps = flyingLapsWithinTime(laps, bestLap.LapTime)
			} else {
				for _, l := range laps {
					if l.Kind == analysis.KindFlying && !l.IsPartialStart {
						goodLaps = append(goodLaps, l)
					}
				}
			}

			if len(goodLaps) > 0 {
				newEntries := analysis.ComputeBrakeEntries(goodLaps, segs)
				for segName, entry := range newEntries {
					pb.BrakeEntrySet(pbf, meta.CarScreenName, meta.TrackDisplayName, segName, entry.Pct, entry.LapsUsed)
				}
				if pbPath != "" {
					if err := pb.Save(pbPath, pbf); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not save pb.json: %v\n", err)
					}
				}
			}

			existingTM.LapsUsed += len(goodLaps)
			existingTM.SessionsUsed++
			existingTM.AddSession(sessionID)
			if trackmapPath != "" {
				if err := trackmap.Save(trackmapPath, tmf); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not save track map: %v\n", err)
				}
			}
		}
	} else if trackLengthM > 0 && bestLap != nil {
		// Auto-detect from flying laps within lapTimeFilterPct of best — slower early
		// laps have different braking points and skew segment boundaries.
		goodLaps := flyingLapsWithinTime(laps, bestLap.LapTime)
		var allSamples [][]trackmap.Sample
		for i := range goodLaps {
			l := &goodLaps[i]
			ts := make([]trackmap.Sample, len(l.Samples))
			for j, s := range l.Samples {
				ts[j] = trackmap.Sample{LapDistPct: s.LapDistPct, Lat: s.Lat, Lon: s.Lon, Speed: s.Speed}
			}
			allSamples = append(allSamples, ts)
		}
		if len(allSamples) == 0 {
			// Fallback: use bestLap only (e.g. all laps are partial-start).
			ts := make([]trackmap.Sample, len(bestLap.Samples))
			for i, s := range bestLap.Samples {
				ts[i] = trackmap.Sample{LapDistPct: s.LapDistPct, Lat: s.Lat, Lon: s.Lon, Speed: s.Speed}
			}
			allSamples = [][]trackmap.Sample{ts}
		}
		// Look up expected corner count from track reference.
		targetCorners := 0
		if n, ok := trf.Corners(meta.TrackDisplayName); ok {
			targetCorners = n
		}

		segs = trackmap.DetectFromMultipleLatLon(allSamples, trackLengthM, targetCorners)
		if segs == nil {
			fmt.Fprintln(os.Stderr, "Warning: Lat/Lon channels not found in telemetry — cannot detect track segments.")
		}

		// Compute brake entries from filtered laps and fold into pb.json.
		if len(segs) > 0 && len(goodLaps) > 0 {
			newEntries := analysis.ComputeBrakeEntries(goodLaps, segs)
			for segName, entry := range newEntries {
				pb.BrakeEntrySet(pbf, meta.CarScreenName, meta.TrackDisplayName, segName, entry.Pct, entry.LapsUsed)
			}
			if pbPath != "" {
				if err := pb.Save(pbPath, pbf); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not save pb.json: %v\n", err)
				}
			}
		}

		if trackmapPath != "" && len(segs) > 0 {
			newTM := &trackmap.TrackMap{
				TrackLengthM: trackLengthM,
				Source:       "auto",
				DetectedFrom: trackmap.Today(),
				GeoMethod:    "latlon",
				LapsUsed:     len(allSamples),
				SessionsUsed: 1,
				Segments:     segs,
			}
			newTM.AddSession(sessionID)
			tmf[meta.TrackDisplayName] = newTM
			if err := trackmap.Save(trackmapPath, tmf); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not save track map: %v\n", err)
			}
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
	//
	// Branch on useExisting, not on matchScore: a stored map is still a stored
	// map even when no match score could be computed (no valid best lap, or the
	// session YAML had no track length). Keying off matchScore made those cases
	// fall through to the "first detection" wording and report a mature map as a
	// fresh low-confidence one, discarding both geomConf and the stored GeoMethod.
	if len(segs) > 0 {
		// used is the stored map when one was loaded, nil on first detection.
		// len(allSamples) is not in scope here, so the lap count for a fresh
		// detection comes from the entry that was just written to tmf.
		var used *trackmap.TrackMap
		if useExisting {
			used = existingTM
		}
		detectedLaps := 1
		if newTM, ok := tmf[meta.TrackDisplayName]; ok {
			detectedLaps = newTM.LapsUsed
		}
		fmt.Print(formatMapLine(len(segs), used, geomConf, matchScore, detectedLaps))

		// Low match score warning.
		if matchScore >= 0 && matchScore < 0.70 {
			fmt.Printf("Warning: lap profile matches stored map at only %.0f%% — consider running with\n", matchScore*100)
			fmt.Println("         -update-map to regenerate segment boundaries from this session.")
			fmt.Println()
		}
	}

	// Resolve brake entries early — needed for both PB phase storage and phase table.
	var brakeEntries pb.BrakeEntryMap
	if meta.CarScreenName != "" && meta.TrackDisplayName != "" {
		if entry := pbf[pb.Key(meta.CarScreenName, meta.TrackDisplayName)]; entry != nil {
			brakeEntries = entry.BrakeEntries
		}
	}

	// Capture the previous PB's phases BEFORE pb.Update overwrites them — when
	// this lap turns out to be a new PB, the vs-PB delta table must compare
	// against the lap we just beat, not against the freshly written entry
	// (which would otherwise compare the lap to itself and show all zeros).
	var pbPhases []pb.PBPhase
	if meta.CarScreenName != "" && meta.TrackDisplayName != "" {
		if entry := pbf[pb.Key(meta.CarScreenName, meta.TrackDisplayName)]; entry != nil {
			pbPhases = entry.Phases
		}
	}

	// PB tracking: check, update, display (pbf already loaded above).
	if pbPath != "" && bestLap != nil && meta.CarScreenName != "" && meta.TrackDisplayName != "" {
		sessionDate := f.DiskHeader().SessionStartDate.Local().Format("2006-01-02")
		weather := analysis.ParseWeather(f.SessionInfo())
		formatted := analysis.FormatLapTime(bestLap.LapTime)

		isNew := pb.Update(pbf, meta.CarScreenName, meta.TrackDisplayName,
			bestLap.LapTime, formatted, sessionDate, weather)

		if isNew {
			// pb.Update replaces the entry wholesale, so any phases/setup stored
			// against the previous PB are gone. They are not carried forward on
			// purpose: they describe a different (slower) lap, and pairing them
			// with the new lap time would make the record self-inconsistent. But
			// the loss must not be silent — without phases the next session has
			// no vs-PB table, and the cause would be invisible.
			if segs != nil {
				pbPhases := phasesToPB(analysis.ComputePhases(bestLap, segs, brakeEntries))
				pb.SetPhases(pbf, meta.CarScreenName, meta.TrackDisplayName, pbPhases)
			} else if len(pbPhases) > 0 {
				fmt.Fprintln(os.Stderr, "Warning: new PB stored without phase data (no track map this session) —")
				fmt.Fprintln(os.Stderr, "         the previous PB's phases were discarded, so the next session has no vs-PB table.")
			}
			if setupBlock := analysis.ExtractCarSetupBlock(f.SessionInfo()); setupBlock != "" {
				pb.SetSetup(pbf, meta.CarScreenName, meta.TrackDisplayName, setupBlock)
			}
			if err := pb.Save(pbPath, pbf); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not save pb.json: %v\n", err)
			}
			fmt.Printf("PB:      %s — set %s, %s  [NEW PB!]\n\n",
				formatted, sessionDate, fallback(weather, "weather unknown"))
		} else {
			stored := pbf[pb.Key(meta.CarScreenName, meta.TrackDisplayName)]
			delta := bestLap.LapTime - stored.LapTime
			fmt.Printf("PB:      %s — set %s, %s  (+%.3fs behind)\n\n",
				stored.LapTimeFormatted, stored.Date,
				fallback(stored.Weather, "weather unknown"), delta)
		}
	}

	fmt.Println("Laps:")
	for _, l := range laps {
		label := l.Kind.String()
		if l.IsCut {
			label += ", cut"
		}
		if l.LapTime > 0 {
			fmt.Printf("  Lap %2d: %s [%s]\n",
				l.Number, analysis.FormatLapTime(l.LapTime), label)
		} else {
			fmt.Printf("  Lap %2d: incomplete [%s]\n", l.Number, label)
		}
	}
	fmt.Println()

	// Sector times use iRacing's own SplitTimeInfo boundaries, so they line up
	// with the sim's timing rather than with MotorHome's detected segments.
	// Absent from some session types, in which case the table is skipped.
	printSectorTable(laps, analysis.ParseSectors(f.SessionInfo()))

	// Dumps go next to the .ibt being analysed. A bare filename would resolve
	// against the current working directory, which is wherever the launcher
	// happened to start us (Stream Deck gives no useful CWD, and it may not
	// even be writable).
	analyzeSingleLap(laps, lapNum, segs, brakeEntries, pbPhases, *dumpSeg, filepath.Dir(ibtPath))
}
