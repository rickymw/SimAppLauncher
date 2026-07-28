package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
		if useExisting {
			lapWord := "lap"
			if existingTM.LapsUsed != 1 {
				lapWord = "laps"
			}
			sessionWord := "session"
			if existingTM.SessionsUsed != 1 {
				sessionWord = "sessions"
			}
			method := existingTM.GeoMethod
			if method == "" {
				method = "latlon"
			}
			matchStr := "n/a (no comparable lap)"
			if matchScore >= 0 {
				matchStr = fmt.Sprintf("%.0f%%", matchScore*100)
			}
			fmt.Printf("Map:     %d segs [%s] — geometry: %s (%d %s, %d %s) — match: %s\n\n",
				len(segs), method, geomConf,
				existingTM.LapsUsed, lapWord,
				existingTM.SessionsUsed, sessionWord,
				matchStr)
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
			fmt.Printf("Map:     %d segs [%s] — geometry: low (%d %s, 1 session) — match: n/a (first detection)\n\n",
				len(segs), "latlon", detectedLaps, lapWord)
		}

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

	// Dumps go next to the .ibt being analysed. A bare filename would resolve
	// against the current working directory, which is wherever the launcher
	// happened to start us (Stream Deck gives no useful CWD, and it may not
	// even be writable).
	analyzeSingleLap(laps, lapNum, segs, brakeEntries, pbPhases, *dumpSeg, filepath.Dir(ibtPath))
}

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

// runStoredPBNoIBT prints the stored PB when no .ibt file was given on the
// command line. Uses the only entry if pb.json has just one; otherwise lists
// the available entries.
func runStoredPBNoIBT(pbPath string) {
	pbf := loadPBOrDie(pbPath)
	if len(pbf) == 1 {
		var entry *pb.PersonalBest
		for _, e := range pbf {
			entry = e
		}
		printStoredPB(entry)
		return
	}
	fmt.Fprintln(os.Stderr, "Multiple PB entries — pass an .ibt file for the session you want, or specify the car/track context:")
	keys := make([]string, 0, len(pbf))
	for k := range pbf {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entry := pbf[k]
		fmt.Fprintf(os.Stderr, "  %s — %s on %s\n", entry.LapTimeFormatted, entry.Car, entry.Track)
	}
	os.Exit(1)
}

// runStoredPBForCarTrack prints the PB stored for car+track, resolved from
// the .ibt's session YAML. Errors out if no PB exists for that combination.
func runStoredPBForCarTrack(pbPath, car, track string) {
	pbf := loadPBOrDie(pbPath)
	entry := pbf[pb.Key(car, track)]
	if entry == nil {
		analyzeDie("no stored PB for %q on %q — drive a flying lap to set one", car, track)
	}
	printStoredPB(entry)
}

// loadPBOrDie loads pb.json and exits with a clear message if it is missing
// or empty.
func loadPBOrDie(pbPath string) pb.File {
	if pbPath == "" {
		analyzeDie("no pb.json path configured")
	}
	pbf, err := pb.Load(pbPath)
	if err != nil {
		analyzeDie("loading pb.json: %v", err)
	}
	if len(pbf) == 0 {
		analyzeDie("no PB entries in %s — drive a flying lap first", pbPath)
	}
	return pbf
}

// printStoredPB renders a stored PB entry: car/track header, setup tables (if
// stored), and the phase table from the saved phases. Used by "analyze -lap pb".
func printStoredPB(entry *pb.PersonalBest) {
	fmt.Printf("Car:   %s\n", fallback(entry.Car, "(unknown)"))
	fmt.Printf("Track: %s\n", fallback(entry.Track, "(unknown)"))
	fmt.Printf("PB:    %s — set %s, %s\n\n",
		entry.LapTimeFormatted, fallback(entry.Date, "?"), fallback(entry.Weather, "weather unknown"))

	if entry.Setup != "" {
		if nodes := analysis.ParseCarSetupTree(entry.Setup); nodes != nil {
			printSetupTables(nodes)
		}
	}

	if len(entry.Phases) == 0 {
		fmt.Println("(no phase data stored for this PB — set a new PB to populate it)")
		return
	}
	printStoredPhaseTable(entry.LapTimeFormatted, entry.Phases)
}

// printStoredPhaseTable mirrors printPhaseTable but works from stored PBPhase
// records (no Lap/Samples context).
func printStoredPhaseTable(lapTimeFormatted string, phases []pb.PBPhase) {
	fmt.Printf("PB lap — %s\n\n", lapTimeFormatted)

	nameW := 4
	for _, p := range phases {
		if len(p.SegName) > nameW {
			nameW = len(p.SegName)
		}
	}
	hdr := fmt.Sprintf(" %-*s | Phase | Spd         | OnBrk | PkBrk | Thr%% | LatG | Wheel° | Corr | ABS  | Lock | Spin | Coast", nameW, "Name")
	sep := fmt.Sprintf("-%s-|-------|-------------|-------|-------|------|------|--------|------|------|------|------|------", dashes(nameW))
	fmt.Println(hdr)
	fmt.Println(sep)
	for _, p := range phases {
		if p.SampleCount == 0 {
			continue
		}
		coastSecs := float32(p.CoastSamples) / 60.0
		fmt.Printf(" %-*s | %-5s | %5.0f→%5.0f | %4.0f%% | %4.0f%% | %3.0f%% | %4.2f | %6.1f | %4d | %4d | %4d | %4d | %5.2fs\n",
			nameW, p.SegName, p.Kind,
			p.SpeedEntryKPH, p.SpeedExitKPH,
			p.BrakePct, p.PeakBrakePct, p.ThrottlePct,
			p.LatGAvg,
			p.PeakSteerDeg, p.Corrections,
			p.ABSCount, p.LockupSamples, p.WheelspinSamples, coastSecs)
	}
	fmt.Println()
}

// ---- single lap ----

// dumpDir is the directory -dump CSVs are written to (normally the directory
// holding the .ibt being analysed).
func analyzeSingleLap(laps []analysis.Lap, lapNum int, segs []trackmap.Segment, brakeEntries pb.BrakeEntryMap, pbPhases []pb.PBPhase, dumpSeg, dumpDir string) {
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
		phases := analysis.ComputePhases(lap, segs, brakeEntries)
		printPhaseTable(lap, phases)
		if len(pbPhases) > 0 {
			printPBComparison(phases, pbPhases)
		}
		printExitImpact(analysis.ComputeExitImpact(segs, phases))
	} else {
		printZoneTable(lap, analysis.ZoneStats(lap))
	}

	// Dump segment telemetry to CSV if requested.
	if dumpSeg != "" {
		if segs == nil {
			analyzeDie("-dump requires a track map (run analyze once first to auto-detect segments)")
		}
		segIdx := analysis.ResolveSegmentName(segs, dumpSeg)
		if segIdx < 0 {
			analyzeDie("segment %q not found — available: %s", dumpSeg, segmentNames(segs))
		}
		csvPath := dumpSegmentPath(dumpDir, segs[segIdx].Name, lap.Number)
		csvFile, err := os.Create(csvPath)
		if err != nil {
			analyzeDie("creating CSV: %v", err)
		}
		defer csvFile.Close()

		cfg := analysis.DefaultDumpConfig()
		if err := analysis.DumpSegmentCSV(csvFile, lap, segs, segIdx, cfg); err != nil {
			analyzeDie("writing CSV: %v", err)
		}
		fmt.Printf("Dumped %s telemetry → %s\n", segs[segIdx].Name, csvPath)
	}
}

// segmentNames returns a comma-separated list of segment names for error messages.
// dumpSegmentPath builds the output path for a -dump CSV. An empty dir yields
// a bare filename (current directory), which is what tests and a caller with no
// .ibt context expect.
func dumpSegmentPath(dir, segName string, lapNumber int) string {
	name := fmt.Sprintf("%s_lap%d.csv", segName, lapNumber)
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

func segmentNames(segs []trackmap.Segment) string {
	names := make([]string, len(segs))
	for i, s := range segs {
		names[i] = s.Name
	}
	return strings.Join(names, ", ")
}

// ---- setup output ----

// cornerNames maps iRacing YAML section names to short column headers.
var cornerNames = map[string]string{
	"LeftFront": "LF", "RightFront": "RF",
	"LeftRear": "LR", "RightRear": "RR",
}

// cornerOrder is the display order for the 4-corner table.
var cornerOrder = []string{"LeftFront", "RightFront", "LeftRear", "RightRear"}

func printSetupTables(nodes []analysis.SetupNode) {
	tires := analysis.FindChild(nodes, "Tires")
	chassis := analysis.FindChild(nodes, "Chassis")

	if tires != nil {
		printCornerTable("Tyres", tires.Children)
	}
	if chassis != nil {
		printCornerTable("Suspension", chassis.Children)
	}
	fmt.Println()
}

// printCornerTable prints a section's per-corner data as an aligned table,
// followed by any non-corner (general) key-value pairs.
func printCornerTable(title string, children []analysis.SetupNode) {
	// Separate corner sections from general sections.
	corners := make(map[string]*analysis.SetupNode)
	var general []analysis.SetupNode
	for i := range children {
		n := &children[i]
		if _, ok := cornerNames[n.Key]; ok {
			corners[n.Key] = n
		} else {
			general = append(general, *n)
		}
	}

	// Normalize equivalent key names (iRacing uses LastTempsOMI for left-side
	// corners and LastTempsIMO for right-side corners — merge into one row).
	keyAliases := map[string]string{
		"LastTempsIMO": "LastTemps",
		"LastTempsOMI": "LastTemps",
	}
	for _, cn := range cornerOrder {
		c := corners[cn]
		if c == nil {
			continue
		}
		for i := range c.Children {
			if alias, ok := keyAliases[c.Children[i].Key]; ok {
				c.Children[i].Key = alias
			}
		}
	}

	// Collect ordered unique keys across all corners.
	var keys []string
	seen := map[string]bool{}
	for _, cn := range cornerOrder {
		c := corners[cn]
		if c == nil {
			continue
		}
		for _, leaf := range c.Children {
			if !seen[leaf.Key] {
				seen[leaf.Key] = true
				keys = append(keys, leaf.Key)
			}
		}
	}

	if len(keys) > 0 {
		// Find the widest label.
		labelW := len(title)
		for _, k := range keys {
			if len(k) > labelW {
				labelW = len(k)
			}
		}

		// Find the widest value per corner column.
		colW := [4]int{2, 2, 2, 2} // min width for "LF" etc
		for ci, cn := range cornerOrder {
			c := corners[cn]
			if c == nil {
				continue
			}
			for _, leaf := range c.Children {
				if len(leaf.Value) > colW[ci] {
					colW[ci] = len(leaf.Value)
				}
			}
		}

		// Print header.
		fmt.Printf("  %-*s", labelW, title+":")
		for ci, cn := range cornerOrder {
			fmt.Printf("  %-*s", colW[ci], cornerNames[cn])
		}
		fmt.Println()

		// Print rows.
		for _, k := range keys {
			fmt.Printf("  %-*s", labelW, k)
			for ci, cn := range cornerOrder {
				c := corners[cn]
				val := ""
				if c != nil {
					if leaf := analysis.FindChild(c.Children, k); leaf != nil {
						val = leaf.Value
					}
				}
				fmt.Printf("  %-*s", colW[ci], val)
			}
			fmt.Println()
		}
	}

	// Print general (non-corner) entries.
	if len(general) > 0 {
		for _, g := range general {
			if g.Value != "" {
				fmt.Printf("  %s: %s\n", g.Key, g.Value)
			} else if len(g.Children) > 0 {
				// Nested non-corner section (e.g. FrontBrakes, InCarDials, Rear).
				for _, leaf := range g.Children {
					if leaf.Value != "" {
						fmt.Printf("  %s: %s\n", leaf.Key, leaf.Value)
					}
				}
			}
		}
	}
	fmt.Println()
}

// ---- output ----

func printZoneTable(lap *analysis.Lap, zones []analysis.Zone) {
	fmt.Printf("Lap %d — %s\n\n", lap.Number, analysis.FormatLapTime(lap.LapTime))
	fmt.Println(" Zone | Dist  | EntSpd | MinSpd | ExtSpd | Brake | Thr  | LatG | ABS | Coast")
	fmt.Println("------|-------|--------|--------|--------|-------|------|------|-----|------")
	for _, z := range zones {
		if z.SampleCount == 0 {
			fmt.Printf("  %2d  | %3d%%  |    --- |    --- |    --- |    -- |   -- |   -- |  -- |   ---\n",
				z.Index+1, (z.Index+1)*5)
			continue
		}
		fmt.Printf("  %2d  | %3d%%  | %6.1f | %6.1f | %6.1f | %5.0f%% | %4.0f%% | %4.2f | %3d | %5d\n",
			z.Index+1, (z.Index+1)*5,
			z.SpeedEntryKPH, z.SpeedMinKPH, z.SpeedExitKPH,
			z.BrakePct, z.ThrottlePct,
			z.LatGAvg,
			z.ABSCount, z.CoastSamples)
	}
	fmt.Println()
}

func printPhaseTable(lap *analysis.Lap, phases []analysis.Phase) {
	fmt.Printf("Lap %d — %s\n\n", lap.Number, analysis.FormatLapTime(lap.LapTime))
	// Find the widest segment name for dynamic column sizing.
	nameW := 4 // minimum "Name"
	for _, p := range phases {
		if len(p.SegName) > nameW {
			nameW = len(p.SegName)
		}
	}

	hdr := fmt.Sprintf(" %-*s | Phase | Spd         | OnBrk | PkBrk | Thr%% | LatG | Wheel° | Corr | ABS  | Lock | Spin | Coast", nameW, "Name")
	sep := fmt.Sprintf("-%s-|-------|-------------|-------|-------|------|------|--------|------|------|------|------|------", dashes(nameW))
	fmt.Println(hdr)
	fmt.Println(sep)
	for _, p := range phases {
		if p.SampleCount == 0 {
			continue
		}
		coastSecs := float32(p.CoastSamples) / 60.0
		fmt.Printf(" %-*s | %-5s | %5.0f→%5.0f | %4.0f%% | %4.0f%% | %3.0f%% | %4.2f | %6.1f | %4d | %4d | %4d | %4d | %5.2fs\n",
			nameW, p.SegName, p.Kind,
			p.SpeedEntryKPH, p.SpeedExitKPH,
			p.BrakePct, p.PeakBrakePct, p.ThrottlePct,
			p.LatGAvg,
			p.PeakSteerDeg, p.Corrections,
			p.ABSCount, p.LockupSamples, p.WheelspinSamples, coastSecs)
	}
	fmt.Println()
}

// printExitImpact prints each corner's exit speed alongside the peak speed
// reached on the straight that follows it — surfaces whether a slow exit is
// costing speed (and time) down the next straight.
func printExitImpact(impacts []analysis.ExitImpact) {
	if len(impacts) == 0 {
		return
	}

	cornerW := 6   // minimum "Corner"
	straightW := 8 // minimum "Straight"
	for _, imp := range impacts {
		if len(imp.CornerName) > cornerW {
			cornerW = len(imp.CornerName)
		}
		if len(imp.StraightName) > straightW {
			straightW = len(imp.StraightName)
		}
	}

	fmt.Println("Corner Exit -> Straight Peak:")
	fmt.Printf("  %-*s  %7s  %-*s  %7s\n", cornerW, "Corner", "ExitSpd", straightW, "Straight", "PeakSpd")
	for _, imp := range impacts {
		fmt.Printf("  %-*s  %6.1f   %-*s  %6.1f\n",
			cornerW, imp.CornerName, imp.CornerExitSpeedKPH,
			straightW, imp.StraightName, imp.StraightPeakSpeedKPH)
	}
	fmt.Println()
}

func printTyreSummary(lap *analysis.Lap) {
	ts := analysis.ComputeTyreSummary(lap)

	// Skip if all temps are zero — channel not present in this file.
	if ts.LF.TempInner == 0 && ts.RF.TempInner == 0 {
		return
	}

	fmt.Printf("Tyres (Lap %d — avg temp, end-of-lap wear):\n", lap.Number)
	fmt.Printf("  %-6s  %-22s  %-21s  %s\n",
		"Corner", "Temp O/M/I (°C)", "Wear O/M/I (% worn)", "Press (kPa)")

	type row struct {
		name string
		c    analysis.CornerTyres
	}
	for _, r := range []row{
		{"LF", ts.LF}, {"RF", ts.RF}, {"LR", ts.LR}, {"RR", ts.RR},
	} {
		wornI := (1 - r.c.WearInner) * 100
		wornM := (1 - r.c.WearMid) * 100
		wornO := (1 - r.c.WearOuter) * 100
		fmt.Printf("  %-6s  %5.1f / %5.1f / %5.1f     %4.2f / %4.2f / %4.2f     %.0f\n",
			r.name,
			r.c.TempOuter, r.c.TempMid, r.c.TempInner,
			wornO, wornM, wornI,
			r.c.PressureKPa)
	}
	fmt.Println()
}

// ---- PB comparison ----

// phasesToPB converts analysis phases to the PB storage format.
func phasesToPB(phases []analysis.Phase) []pb.PBPhase {
	out := make([]pb.PBPhase, len(phases))
	for i, p := range phases {
		out[i] = pb.PBPhase{
			SegName:          p.SegName,
			Kind:             string(p.Kind),
			SpeedEntryKPH:    p.SpeedEntryKPH,
			SpeedExitKPH:     p.SpeedExitKPH,
			BrakePct:         p.BrakePct,
			PeakBrakePct:     p.PeakBrakePct,
			ThrottlePct:      p.ThrottlePct,
			LatGAvg:          p.LatGAvg,
			PeakSteerDeg:     p.PeakSteerDeg,
			Corrections:      p.Corrections,
			ABSCount:         p.ABSCount,
			LockupSamples:    p.LockupSamples,
			WheelspinSamples: p.WheelspinSamples,
			CoastSamples:     p.CoastSamples,
			SampleCount:      p.SampleCount,
		}
	}
	return out
}

// printPBComparison prints a delta table comparing the current lap's phases
// against stored PB phases. Positive speed deltas = faster than PB (good).
// Positive brake/coast deltas = more than PB (usually bad).
func printPBComparison(current []analysis.Phase, stored []pb.PBPhase) {
	lookup := pb.PhaseLookup(stored)

	// Check if any current phase matches a stored phase.
	hasMatch := false
	for _, p := range current {
		if _, ok := lookup[pb.PhaseKey(p.SegName, string(p.Kind))]; ok {
			hasMatch = true
			break
		}
	}
	if !hasMatch {
		return
	}

	// Find the widest segment name.
	nameW := 4
	for _, p := range current {
		if len(p.SegName) > nameW {
			nameW = len(p.SegName)
		}
	}

	fmt.Println("vs PB:")
	fmt.Println()
	hdr := fmt.Sprintf(" %-*s | Phase | dSpd        | dBrk  | dPkBr | dThr | dLatG  | dCorr | dABS | dLck | dSpn | dCoast", nameW, "Name")
	sep := fmt.Sprintf("-%s-|-------|-------------|-------|-------|------|--------|-------|------|------|------|-------", dashes(nameW))
	fmt.Println(hdr)
	fmt.Println(sep)

	for _, p := range current {
		if p.SampleCount == 0 {
			continue
		}
		key := pb.PhaseKey(p.SegName, string(p.Kind))
		ref, ok := lookup[key]
		if !ok {
			// No matching PB phase — skip row.
			continue
		}

		dSpdIn := p.SpeedEntryKPH - ref.SpeedEntryKPH
		dSpdOut := p.SpeedExitKPH - ref.SpeedExitKPH
		dBrk := p.BrakePct - ref.BrakePct
		dPkBr := p.PeakBrakePct - ref.PeakBrakePct
		dThr := p.ThrottlePct - ref.ThrottlePct
		dLatG := p.LatGAvg - ref.LatGAvg
		dCorr := p.Corrections - ref.Corrections
		dABS := p.ABSCount - ref.ABSCount
		dLck := p.LockupSamples - ref.LockupSamples
		dSpn := p.WheelspinSamples - ref.WheelspinSamples
		dCoast := float32(p.CoastSamples-ref.CoastSamples) / 60.0

		fmt.Printf(" %-*s | %-5s | %+5.0f→%+5.0f | %+5.0f | %+5.0f | %+4.0f | %+5.2f | %+5d | %+4d | %+4d | %+4d | %+6.2fs\n",
			nameW, p.SegName, p.Kind,
			dSpdIn, dSpdOut,
			dBrk, dPkBr, dThr,
			dLatG,
			dCorr, dABS, dLck, dSpn, dCoast)
	}
	fmt.Println()
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
