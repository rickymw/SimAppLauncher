package main

// All terminal rendering for the analyze subcommand: the single-lap driver,
// setup tables, and the zone / phase / exit-impact / tyre / vs-PB tables.

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// ---- single lap ----

// singleLapOpts carries everything the per-lap output stage needs. It is a
// struct rather than a parameter list because the stage now renders four
// optional tables plus two dump modes, and positional arguments for that many
// optional inputs are easy to transpose silently.
type singleLapOpts struct {
	laps         []analysis.Lap
	lapNum       int // 0 = best lap of session
	segs         []trackmap.Segment
	brakeEntries pb.BrakeEntryMap
	pbPhases     []pb.PBPhase

	// consistency is the lap-to-lap spread across comparableLaps; both are
	// empty/zero when there were too few comparable laps to measure spread.
	consistency    []analysis.ConsistencyRow
	comparableLaps []analysis.Lap

	locatedNotes    []analysis.LocatedNote
	notesSourceFile string

	dumpSeg     string
	dumpDir     string
	dumpAllLaps bool // -dump-all: every comparable lap in one CSV
	dumpHz      int  // -hz override; 0 = DefaultDumpConfig's 20Hz
}

// analyzeSingleLap renders the per-lap tables and handles -dump.
//
// opts.dumpDir is the directory -dump CSVs are written to (normally the
// directory holding the .ibt being analysed).
func analyzeSingleLap(opts singleLapOpts) {
	var lap *analysis.Lap
	if opts.lapNum > 0 {
		lap = findAnalyzeLap(opts.laps, opts.lapNum)
		if lap == nil {
			analyzeDie("lap %d not found in file", opts.lapNum)
		}
		if lap.Kind != analysis.KindFlying {
			aprintf("Note: Lap %d is a %s — data includes pit lane or standing start.\n\n",
				lap.Number, lap.Kind)
		}
		if lap.IsPartialStart {
			aprintf("Note: Lap %d started mid-recording — lap time is underestimated.\n\n",
				lap.Number)
		}
	} else {
		lap = bestAnalyzeLap(opts.laps)
		if lap == nil {
			analyzeDie("no flying laps found in file (all laps are out laps or in laps)")
		}
		aprintf("Selecting best lap: Lap %d (%s)\n\n",
			lap.Number, analysis.FormatLapTime(lap.LapTime))
	}

	if opts.segs != nil {
		phases := analysis.ComputePhases(lap, opts.segs, opts.brakeEntries)
		printPhaseTable(lap, phases)
		if len(opts.pbPhases) > 0 {
			printPBComparison(phases, opts.pbPhases)
		}
		printExitImpact(analysis.ComputeExitImpact(opts.segs, phases))
	} else {
		printZoneTable(lap, analysis.ZoneStats(lap))
	}

	printConsistency(opts.consistency, lapNumbers(opts.comparableLaps))
	printNotes(opts.locatedNotes, opts.notesSourceFile)

	// Dump segment telemetry to CSV if requested.
	if opts.dumpSeg != "" {
		runDump(opts, lap)
	}
}

// runDump writes the -dump CSV for one segment, either for the single lap being
// analysed or — with -dump-all — for every comparable lap in one file.
func runDump(opts singleLapOpts, lap *analysis.Lap) {
	if opts.segs == nil {
		analyzeDie("-dump requires a track map (run analyze once first to auto-detect segments)")
	}
	segIdx := analysis.ResolveSegmentName(opts.segs, opts.dumpSeg)
	if segIdx < 0 {
		analyzeDie("segment %q not found — available: %s", opts.dumpSeg, segmentNames(opts.segs))
	}
	segName := opts.segs[segIdx].Name
	cfg := analysis.DefaultDumpConfig()
	if opts.dumpHz > 0 {
		cfg.DownsampleRate = analysis.DownsampleRateForHz(opts.dumpHz)
	}

	if opts.dumpAllLaps {
		dumpLaps := opts.comparableLaps
		if len(dumpLaps) == 0 {
			analyzeDie("-dump-all found no comparable flying laps — drop the flag to dump lap %d alone", lap.Number)
		}
		csvPath := dumpSegmentAllLapsPath(opts.dumpDir, segName)
		csvFile, err := os.Create(csvPath)
		if err != nil {
			analyzeDie("creating CSV: %v", err)
		}
		defer csvFile.Close()

		if err := analysis.DumpSegmentAllLapsCSV(csvFile, dumpLaps, opts.segs, segIdx, cfg); err != nil {
			analyzeDie("writing CSV: %v", err)
		}
		aprintf("Dumped %s telemetry for %d laps → %s\n", segName, len(dumpLaps), csvPath)
		return
	}

	csvPath := dumpSegmentPath(opts.dumpDir, segName, lap.Number)
	csvFile, err := os.Create(csvPath)
	if err != nil {
		analyzeDie("creating CSV: %v", err)
	}
	defer csvFile.Close()

	if err := analysis.DumpSegmentCSV(csvFile, lap, opts.segs, segIdx, cfg); err != nil {
		analyzeDie("writing CSV: %v", err)
	}
	aprintf("Dumped %s telemetry → %s\n", segName, csvPath)
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
	aprintln()
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
		aprintf("  %-*s", labelW, title+":")
		for ci, cn := range cornerOrder {
			aprintf("  %-*s", colW[ci], cornerNames[cn])
		}
		aprintln()

		// Print rows.
		for _, k := range keys {
			aprintf("  %-*s", labelW, k)
			for ci, cn := range cornerOrder {
				c := corners[cn]
				val := ""
				if c != nil {
					if leaf := analysis.FindChild(c.Children, k); leaf != nil {
						val = leaf.Value
					}
				}
				aprintf("  %-*s", colW[ci], val)
			}
			aprintln()
		}
	}

	// Print general (non-corner) entries.
	if len(general) > 0 {
		for _, g := range general {
			if g.Value != "" {
				aprintf("  %s: %s\n", g.Key, g.Value)
			} else if len(g.Children) > 0 {
				// Nested non-corner section (e.g. FrontBrakes, InCarDials, Rear).
				for _, leaf := range g.Children {
					if leaf.Value != "" {
						aprintf("  %s: %s\n", leaf.Key, leaf.Value)
					}
				}
			}
		}
	}
	aprintln()
}

// ---- output ----

func printZoneTable(lap *analysis.Lap, zones []analysis.Zone) {
	aprintf("Lap %d — %s\n\n", lap.Number, analysis.FormatLapTime(lap.LapTime))
	aprintln(" Zone | Dist  | EntSpd | MinSpd | ExtSpd | Brake | Thr  | LatG | ABS | Coast")
	aprintln("------|-------|--------|--------|--------|-------|------|------|-----|------")
	for _, z := range zones {
		if z.SampleCount == 0 {
			aprintf("  %2d  | %3d%%  |    --- |    --- |    --- |    -- |   -- |   -- |  -- |   ---\n",
				z.Index+1, (z.Index+1)*5)
			continue
		}
		aprintf("  %2d  | %3d%%  | %6.1f | %6.1f | %6.1f | %5.0f%% | %4.0f%% | %4.2f | %3d | %5d\n",
			z.Index+1, (z.Index+1)*5,
			z.SpeedEntryKPH, z.SpeedMinKPH, z.SpeedExitKPH,
			z.BrakePct, z.ThrottlePct,
			z.LatGAvg,
			z.ABSCount, z.CoastSamples)
	}
	aprintln()
}

func printPhaseTable(lap *analysis.Lap, phases []analysis.Phase) {
	aprintf("Lap %d — %s\n\n", lap.Number, analysis.FormatLapTime(lap.LapTime))
	// Find the widest segment name for dynamic column sizing.
	nameW := 4 // minimum "Name"
	for _, p := range phases {
		if len(p.SegName) > nameW {
			nameW = len(p.SegName)
		}
	}

	hdr := fmt.Sprintf(" %-*s | Phase | Spd         | OnBrk | PkBrk | Thr%% | LatG | Wheel° | Corr | ABS  | Lock | Spin | Coast", nameW, "Name")
	sep := fmt.Sprintf("-%s-|-------|-------------|-------|-------|------|------|--------|------|------|------|------|------", dashes(nameW))
	aprintln(hdr)
	aprintln(sep)
	for _, p := range phases {
		if p.SampleCount == 0 {
			continue
		}
		coastSecs := float32(p.CoastSamples) / 60.0
		aprintf(" %-*s | %-5s | %5.0f→%5.0f | %4.0f%% | %4.0f%% | %3.0f%% | %4.2f | %6.1f | %4d | %4d | %4d | %4d | %5.2fs\n",
			nameW, p.SegName, p.Kind,
			p.SpeedEntryKPH, p.SpeedExitKPH,
			p.BrakePct, p.PeakBrakePct, p.ThrottlePct,
			p.LatGAvg,
			p.PeakSteerDeg, p.Corrections,
			p.ABSCount, p.LockupSamples, p.WheelspinSamples, coastSecs)
	}
	aprintln()
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

	aprintln("Corner Exit -> Straight Peak:")
	aprintf("  %-*s  %7s  %-*s  %7s\n", cornerW, "Corner", "ExitSpd", straightW, "Straight", "PeakSpd")
	for _, imp := range impacts {
		aprintf("  %-*s  %6.1f   %-*s  %6.1f\n",
			cornerW, imp.CornerName, imp.CornerExitSpeedKPH,
			straightW, imp.StraightName, imp.StraightPeakSpeedKPH)
	}
	aprintln()
}

// ---- fuel ----

// printFuel prints fuel consumption and what it implies for stint length.
//
// planLaps > 0 adds a line answering "can I finish a race of this many laps",
// which is the question the numbers exist to serve.
func printFuel(f analysis.FuelSummary, planLaps int) {
	if !f.Available {
		return
	}

	aprintln("Fuel:")
	aprintf("  Used:      %.2f L total; %.2f avg, %.2f median, %.2f worst per lap (%d measured %s)\n",
		f.UsedLitres, f.AvgPerLap, f.MedianPerLap, f.WorstPerLap,
		f.LapsMeasured, pluralize(f.LapsMeasured, "lap", "laps"))

	// Both figures, because planning a stint on the average runs dry half the
	// time — the worst-lap rate is the one a stint has to survive.
	aprintf("  Remaining: %.2f L → %.1f laps at average, %.1f at worst-lap rate\n",
		f.EndLitres, f.LapsRemainingAvg, f.LapsRemainingWorst)

	if f.Refuelled {
		aprintln("             (a lap refuelled — remaining is measured at the end of the last lap that didn't)")
	}

	if f.AvgUsePerHour > 0 {
		aprintf("  Burn rate: %.1f kg/h average\n", f.AvgUsePerHour)
	}

	if planLaps > 0 {
		need := analysis.FuelForLaps(f.WorstPerLap, planLaps, fuelMarginLaps)
		aprintf("  For %d laps: %.2f L at the worst-lap rate (+%.0f lap margin)",
			planLaps, need, fuelMarginLaps)
		if need > f.EndLitres {
			aprintf(" — %.2f L short of what's in the tank\n", need-f.EndLitres)
		} else {
			aprintf(" — %.2f L in hand\n", f.EndLitres-need)
		}
	}
	aprintln()
}

// ---- consistency ----

// printConsistency prints the lap-to-lap spread of each segment phase, plus a
// short list of the phases that vary most.
//
// The phase table above it describes a single lap; this describes how
// repeatable that lap is. Speeds are shown as mean ± SD, while brake/LatG/coast
// show SD only — their means are already in the phase table and the spread is
// the new information.
func printConsistency(rows []analysis.ConsistencyRow, lapNums []int) {
	if len(rows) == 0 {
		return
	}

	nameW := 4 // minimum "Name"
	for _, r := range rows {
		if len(r.SegName) > nameW {
			nameW = len(r.SegName)
		}
	}

	// Name the laps rather than just counting them: the spread means nothing
	// without knowing which laps went into it, and the filtered population is
	// not the same as the lap list printed above.
	nums := make([]string, len(lapNums))
	for i, n := range lapNums {
		nums[i] = strconv.Itoa(n)
	}
	aprintf("Consistency (%d %s: %s):\n\n",
		len(lapNums), pluralize(len(lapNums), "lap", "laps"), strings.Join(nums, ", "))
	aprintf(" %-*s | Phase | N  | EntSpd      | ExitSpd     | PkBrk | LatG  | Coast | Best exit\n", nameW, "Name")
	aprintf("-%s-|-------|----|-------------|-------------|-------|-------|-------|-----------\n", dashes(nameW))
	for _, r := range rows {
		aprintf(" %-*s | %-5s | %2d | %5.1f ±%4.1f | %5.1f ±%4.1f | ±%4.1f | ±%4.2f | ±%4.2f | %5.1f (L%d)\n",
			nameW, r.SegName, r.Kind, r.Laps,
			r.EntrySpeedMean, r.EntrySpeedSD,
			r.ExitSpeedMean, r.ExitSpeedSD,
			r.PeakBrakeSD, r.LatGSD, r.CoastSD,
			r.BestExitSpeedKPH, r.BestExitLap)
	}
	aprintln()

	if top := analysis.MostVariable(rows, 3); len(top) > 0 && top[0].ExitSpeedSD > 0 {
		aprint("  Most variable exit speed:")
		for i, r := range top {
			if r.ExitSpeedSD <= 0 {
				break
			}
			if i > 0 {
				aprint(",")
			}
			aprintf(" %s %s (±%.1f km/h)", r.SegName, r.Kind, r.ExitSpeedSD)
		}
		aprintln()
		aprintln()
	}
}

func printTyreSummary(lap *analysis.Lap) {
	ts := analysis.ComputeTyreSummary(lap)

	// Skip if all temps are zero — channel not present in this file.
	if ts.LF.TempInner == 0 && ts.RF.TempInner == 0 {
		return
	}

	aprintf("Tyres (Lap %d — avg temp, end-of-lap wear):\n", lap.Number)
	aprintf("  %-6s  %-22s  %-21s  %s\n",
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
		aprintf("  %-6s  %5.1f / %5.1f / %5.1f     %4.2f / %4.2f / %4.2f     %.0f\n",
			r.name,
			r.c.TempOuter, r.c.TempMid, r.c.TempInner,
			wornO, wornM, wornI,
			r.c.PressureKPa)
	}
	aprintln()
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

	aprintln("vs PB:")
	aprintln()
	hdr := fmt.Sprintf(" %-*s | Phase | dSpd        | dBrk  | dPkBr | dThr | dLatG  | dCorr | dABS | dLck | dSpn | dCoast", nameW, "Name")
	sep := fmt.Sprintf("-%s-|-------|-------------|-------|-------|------|--------|-------|------|------|------|-------", dashes(nameW))
	aprintln(hdr)
	aprintln(sep)

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

		aprintf(" %-*s | %-5s | %+5.0f→%+5.0f | %+5.0f | %+5.0f | %+4.0f | %+5.2f | %+5d | %+4d | %+4d | %+4d | %+6.2fs\n",
			nameW, p.SegName, p.Kind,
			dSpdIn, dSpdOut,
			dBrk, dPkBr, dThr,
			dLatG,
			dCorr, dABS, dLck, dSpn, dCoast)
	}
	aprintln()
}

// ---- sector times ----

// printSectorTable prints per-sector times for each flying lap, plus a best
// row showing the fastest time in each sector and the theoretical best lap.
//
// Sectors come from iRacing's own SplitTimeInfo, so these boundaries match the
// sim's timing rather than MotorHome's detected segments — which is the point:
// it localises where time is being lost before the per-corner tables are read.
func printSectorTable(laps []analysis.Lap, sectors []analysis.Sector) {
	if len(sectors) == 0 {
		return
	}

	var rows [][]analysis.SectorTime
	var nums []int
	var lapTimes []float32
	for i := range laps {
		l := &laps[i]
		if l.Kind != analysis.KindFlying || l.IsPartialStart || l.LapTime <= 0 {
			continue
		}
		st := analysis.ComputeSectorTimes(l, sectors)
		if len(st) == 0 {
			continue
		}
		rows = append(rows, st)
		nums = append(nums, l.Number)
		lapTimes = append(lapTimes, l.LapTime)
	}
	if len(rows) == 0 {
		return
	}

	best, from := analysis.BestSectorTimes(rows, nums)

	aprintln("Sectors:")
	aprintln()
	aprintf("  %-6s", "Lap")
	for i := range sectors {
		aprintf(" %9s", fmt.Sprintf("S%d", i+1))
	}
	aprintf("  %10s\n", "Lap")
	aprintf("  %-6s", "------")
	for range sectors {
		aprintf(" %9s", "---------")
	}
	aprintf("  %10s\n", "----------")

	for ri, st := range rows {
		aprintf("  %-6d", nums[ri])
		for i := range sectors {
			if i >= len(st) || !st[i].Complete {
				aprintf(" %9s", "--")
				continue
			}
			// Mark the session-best time in each sector.
			mark := " "
			if best[i].Complete && st[i].Seconds == best[i].Seconds && from[i] == nums[ri] {
				mark = "*"
			}
			aprintf(" %8.3f%s", st[i].Seconds, mark)
		}
		// The lap column is the official LapLastLapTime, not the sum of the
		// sectors above it. Summing gives a value a few ms different (boundary
		// crossings are interpolated), which next to the lap list would read as
		// a bug rather than as rounding.
		aprintf("  %10s\n", analysis.FormatLapTime(lapTimes[ri]))
	}

	// Theoretical best: the sum of the fastest sector times across all laps.
	var theoretical float32
	complete := true
	aprintf("  %-6s", "best")
	for i := range sectors {
		if i >= len(best) || !best[i].Complete {
			aprintf(" %9s", "--")
			complete = false
			continue
		}
		theoretical += best[i].Seconds
		aprintf(" %9.3f", best[i].Seconds)
	}
	if complete {
		aprintf("  %10s\n", analysis.FormatLapTime(theoretical))
	} else {
		aprintf("  %10s\n", "--")
	}
	aprintln()
	if complete {
		aprintf("  Theoretical best %s from sectors set on laps", analysis.FormatLapTime(theoretical))
		for i := range best {
			if from[i] >= 0 {
				aprintf(" %d", from[i])
			}
		}
		aprintln()
		aprintln()
	}
}
