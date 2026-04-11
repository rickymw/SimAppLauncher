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
	lapNum := fs.Int("lap", 0, "lap number to analyze (0 = best completed lap)")
	updateMap := fs.Bool("update-map", false, "ignore existing track map and re-detect from this session")
	dumpSeg := fs.String("dump", "", "dump segment telemetry to CSV (name like T3 or 1-based index)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] analyze [flags] <file.ibt>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  motorhome analyze session.ibt")
		fmt.Fprintln(os.Stderr, "  motorhome analyze -lap 2 session.ibt")
		fmt.Fprintln(os.Stderr, "  motorhome analyze -dump T3 session.ibt")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	var ibtPath string
	switch fs.NArg() {
	case 0:
		if cfg.IbtDir == "" {
			fs.Usage()
			os.Exit(1)
		}
		var err error
		ibtPath, err = nthLatestIbtFile(cfg.IbtDir, 1)
		if err != nil {
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
			method := existingTM.GeoMethod
			if method == "" {
				method = "latlon"
			}
			fmt.Printf("Map:     %d segs [%s] — geometry: %s (%d %s, %d %s) — match: %.0f%%\n\n",
				len(segs), method, geomConf,
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

	// PB tracking: check, update, display (pbf already loaded above).
	if pbPath != "" && bestLap != nil && meta.CarScreenName != "" && meta.TrackDisplayName != "" {
		sessionDate := f.DiskHeader().SessionStartDate.Local().Format("2006-01-02")
		weather := analysis.ParseWeather(f.SessionInfo())
		formatted := analysis.FormatLapTime(bestLap.LapTime)

		isNew := pb.Update(pbf, meta.CarScreenName, meta.TrackDisplayName,
			bestLap.LapTime, formatted, sessionDate, weather)

		if isNew {
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
		if l.LapTime > 0 {
			fmt.Printf("  Lap %2d: %s [%s]\n",
				l.Number, analysis.FormatLapTime(l.LapTime), l.Kind)
		} else {
			fmt.Printf("  Lap %2d: incomplete [%s]\n", l.Number, l.Kind)
		}
	}
	fmt.Println()

	var brakeEntries pb.BrakeEntryMap
	if meta.CarScreenName != "" && meta.TrackDisplayName != "" {
		if entry := pbf[pb.Key(meta.CarScreenName, meta.TrackDisplayName)]; entry != nil {
			brakeEntries = entry.BrakeEntries
		}
	}
	analyzeSingleLap(laps, *lapNum, segs, brakeEntries, *dumpSeg)
}

// ---- single lap ----

func analyzeSingleLap(laps []analysis.Lap, lapNum int, segs []trackmap.Segment, brakeEntries pb.BrakeEntryMap, dumpSeg string) {
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
		csvName := fmt.Sprintf("%s_lap%d.csv", segs[segIdx].Name, lap.Number)
		csvFile, err := os.Create(csvName)
		if err != nil {
			analyzeDie("creating CSV: %v", err)
		}
		defer csvFile.Close()

		cfg := analysis.DefaultDumpConfig()
		if err := analysis.DumpSegmentCSV(csvFile, lap, segs, segIdx, cfg); err != nil {
			analyzeDie("writing CSV: %v", err)
		}
		fmt.Printf("Dumped %s telemetry → %s\n", segs[segIdx].Name, csvName)
	}
}

// segmentNames returns a comma-separated list of segment names for error messages.
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

// flyingLapsWithinTime returns flying, non-partial-start laps whose lap time is
// within lapTimeFilterDelta of bestTime. This excludes early slow laps that would
// skew corner boundaries and brake-entry positions.
// Falls back to all valid flying laps only if none pass the filter.
func flyingLapsWithinTime(laps []analysis.Lap, bestTime float32) []analysis.Lap {
	threshold := bestTime + lapTimeFilterDelta
	var result []analysis.Lap
	for i := range laps {
		l := &laps[i]
		if l.Kind != analysis.KindFlying || l.IsPartialStart || l.LapTime <= 0 {
			continue
		}
		if l.LapTime <= threshold {
			result = append(result, *l)
		}
	}
	if len(result) == 0 {
		// Fallback: return all valid flying laps (bestLap itself always passes
		// since threshold = bestTime + delta >= bestTime).
		for i := range laps {
			l := &laps[i]
			if l.Kind == analysis.KindFlying && !l.IsPartialStart && l.LapTime > 0 {
				result = append(result, *l)
			}
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
		if e.IsDir() || filepath.Ext(e.Name()) != ".ibt" {
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

