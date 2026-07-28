package main

// All terminal rendering for the analyze subcommand: the single-lap driver,
// setup tables, and the zone / phase / exit-impact / tyre / vs-PB tables.

import (
	"fmt"
	"os"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

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

	fmt.Println("Sectors:")
	fmt.Println()
	fmt.Printf("  %-6s", "Lap")
	for i := range sectors {
		fmt.Printf(" %9s", fmt.Sprintf("S%d", i+1))
	}
	fmt.Printf("  %10s\n", "Lap")
	fmt.Printf("  %-6s", "------")
	for range sectors {
		fmt.Printf(" %9s", "---------")
	}
	fmt.Printf("  %10s\n", "----------")

	for ri, st := range rows {
		fmt.Printf("  %-6d", nums[ri])
		for i := range sectors {
			if i >= len(st) || !st[i].Complete {
				fmt.Printf(" %9s", "--")
				continue
			}
			// Mark the session-best time in each sector.
			mark := " "
			if best[i].Complete && st[i].Seconds == best[i].Seconds && from[i] == nums[ri] {
				mark = "*"
			}
			fmt.Printf(" %8.3f%s", st[i].Seconds, mark)
		}
		// The lap column is the official LapLastLapTime, not the sum of the
		// sectors above it. Summing gives a value a few ms different (boundary
		// crossings are interpolated), which next to the lap list would read as
		// a bug rather than as rounding.
		fmt.Printf("  %10s\n", analysis.FormatLapTime(lapTimes[ri]))
	}

	// Theoretical best: the sum of the fastest sector times across all laps.
	var theoretical float32
	complete := true
	fmt.Printf("  %-6s", "best")
	for i := range sectors {
		if i >= len(best) || !best[i].Complete {
			fmt.Printf(" %9s", "--")
			complete = false
			continue
		}
		theoretical += best[i].Seconds
		fmt.Printf(" %9.3f", best[i].Seconds)
	}
	if complete {
		fmt.Printf("  %10s\n", analysis.FormatLapTime(theoretical))
	} else {
		fmt.Printf("  %10s\n", "--")
	}
	fmt.Println()
	if complete {
		fmt.Printf("  Theoretical best %s from sectors set on laps", analysis.FormatLapTime(theoretical))
		for i := range best {
			if from[i] >= 0 {
				fmt.Printf(" %d", from[i])
			}
		}
		fmt.Println()
		fmt.Println()
	}
}
