package main

// Conversion from the analyze pipeline's computed values into the -json
// document defined in analyze_json.go.

import (
	"path/filepath"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
)

// buildAnalyzeResult assembles the JSON document. It recomputes phases for the
// analysed lap rather than threading them out of the rendering stage — the
// computation is cheap next to reading the .ibt, and duplicating the *values*
// would risk the two views disagreeing.
func buildAnalyzeResult(in analyzeResultInput) analyzeResult {
	res := analyzeResult{
		Schema:      analyzeSchema,
		File:        filepath.Base(in.ibtPath),
		Driver:      in.meta.DriverName,
		Car:         in.meta.CarScreenName,
		Track:       in.meta.TrackDisplayName,
		SessionDate: in.sessionDate.Local().Format("2006-01-02T15:04:05Z07:00"),
		Samples:     in.sampleCount,
		TickRateHz:  in.tickRate,
		Traces:      in.traces,
	}

	comparable := map[int]bool{}
	for _, l := range in.comparableLaps {
		comparable[l.Number] = true
	}

	res.Laps = make([]jsonLap, 0, len(in.laps))
	for i := range in.laps {
		l := &in.laps[i]
		jl := jsonLap{
			Number:       l.Number,
			Kind:         l.Kind.String(),
			Cut:          l.IsCut,
			PartialStart: l.IsPartialStart,
			Complete:     l.LapTime > 0,
			Comparable:   comparable[l.Number],
		}
		if l.LapTime > 0 {
			jl.LapTime = l.LapTime
			jl.TimeFormatted = analysis.FormatLapTime(l.LapTime)
		}
		res.Laps = append(res.Laps, jl)
	}

	if len(in.segs) > 0 {
		tm := &jsonTrackMap{Segments: in.segs}
		if in.trackMap != nil {
			tm.GeoMethod = in.trackMap.GeoMethod
			tm.LapsUsed = in.trackMap.LapsUsed
			tm.SessionsUsed = in.trackMap.SessionsUsed
		}
		if in.geomConf != "" {
			tm.Confidence = string(in.geomConf)
		}
		if in.matchScore >= 0 {
			score := in.matchScore
			tm.MatchScore = &score
		}
		res.TrackMap = tm
	}

	lap := selectAnalyzeLap(in.laps, in.lapNum)

	if in.pbEntry != nil && in.pbEntry.LapTime > 0 {
		jp := &jsonPB{
			LapTime:          in.pbEntry.LapTime,
			LapTimeFormatted: in.pbEntry.LapTimeFormatted,
			Date:             in.pbEntry.Date,
			Weather:          in.pbEntry.Weather,
		}
		if best := bestAnalyzeLap(in.laps); best != nil {
			jp.DeltaToBest = best.LapTime - in.pbEntry.LapTime
		}
		res.PB = jp
	}

	res.Sectors = buildJSONSectors(in.laps, in.sectors)

	if lap != nil {
		res.AnalysedLap = buildJSONAnalysedLap(lap, in)
	}

	for _, c := range in.consistency {
		res.Consistency = append(res.Consistency, jsonConsistency{
			Segment:        c.SegName,
			Phase:          string(c.Kind),
			Laps:           c.Laps,
			EntrySpeedMean: c.EntrySpeedMean,
			EntrySpeedSD:   c.EntrySpeedSD,
			ExitSpeedMean:  c.ExitSpeedMean,
			ExitSpeedSD:    c.ExitSpeedSD,
			PeakBrakeMean:  c.PeakBrakeMean,
			PeakBrakeSD:    c.PeakBrakeSD,
			LatGMean:       c.LatGMean,
			LatGSD:         c.LatGSD,
			CoastMean:      c.CoastMean,
			CoastSD:        c.CoastSD,
			BestExitKPH:    c.BestExitSpeedKPH,
			BestExitLap:    c.BestExitLap,
		})
	}

	if in.fuel.Available {
		jf := &jsonFuel{
			StartLitres:        in.fuel.StartLitres,
			EndLitres:          in.fuel.EndLitres,
			UsedLitres:         in.fuel.UsedLitres,
			Refuelled:          in.fuel.Refuelled,
			LapsMeasured:       in.fuel.LapsMeasured,
			AvgPerLap:          in.fuel.AvgPerLap,
			MedianPerLap:       in.fuel.MedianPerLap,
			WorstPerLap:        in.fuel.WorstPerLap,
			AvgUsePerHour:      in.fuel.AvgUsePerHour,
			LapsRemainingAvg:   in.fuel.LapsRemainingAvg,
			LapsRemainingWorst: in.fuel.LapsRemainingWorst,
		}
		for _, lf := range in.fuel.PerLap {
			jf.PerLap = append(jf.PerLap, jsonLapFuel{
				Lap:         lf.LapNumber,
				StartLitres: lf.StartLitres,
				EndLitres:   lf.EndLitres,
				UsedLitres:  lf.UsedLitres,
				Refuelled:   lf.Refuelled,
			})
		}
		res.Fuel = jf
	}

	for _, n := range in.notes {
		jn := jsonNote{
			Text:    n.Text,
			At:      n.At.Local().Format("2006-01-02T15:04:05Z07:00"),
			Located: n.Located,
		}
		if n.Located {
			jn.Lap = n.LapNumber
			jn.LapDistPct = n.LapDistPct
			jn.Segment = n.SegName
		}
		res.Notes = append(res.Notes, jn)
	}

	return res
}

// buildJSONAnalysedLap renders the per-lap section: phases, vs-PB deltas, exit
// impact and tyres — or the zone fallback when there is no track map.
func buildJSONAnalysedLap(lap *analysis.Lap, in analyzeResultInput) *jsonAnalysedLap {
	out := &jsonAnalysedLap{
		Number:        lap.Number,
		LapTime:       lap.LapTime,
		TimeFormatted: analysis.FormatLapTime(lap.LapTime),
		Selection:     "best",
	}
	if in.lapNum > 0 {
		out.Selection = "explicit"
	}

	if ts := analysis.ComputeTyreSummary(lap); ts.LF.TempInner != 0 || ts.RF.TempInner != 0 {
		out.Tyres = &jsonTyreSummary{
			BrakeBias: ts.BrakeBias,
			Corners: map[string]jsonCorner{
				"LF": jsonCornerFrom(ts.LF),
				"RF": jsonCornerFrom(ts.RF),
				"LR": jsonCornerFrom(ts.LR),
				"RR": jsonCornerFrom(ts.RR),
			},
		}
	}

	if len(in.segs) == 0 {
		out.Zones = analysis.ZoneStats(lap)
		return out
	}

	phases := analysis.ComputePhases(lap, in.segs, in.brakeEntries)
	for _, p := range phases {
		if p.SampleCount == 0 {
			continue
		}
		out.Phases = append(out.Phases, jsonPhase{
			Segment:       p.SegName,
			SegmentIndex:  p.SegIndex,
			Phase:         string(p.Kind),
			SpeedEntryKPH: p.SpeedEntryKPH,
			SpeedExitKPH:  p.SpeedExitKPH,
			PeakSpeedKPH:  p.PeakSpeedKPH,
			BrakePct:      p.BrakePct,
			PeakBrakePct:  p.PeakBrakePct,
			ThrottlePct:   p.ThrottlePct,
			LatGAvg:       p.LatGAvg,
			PeakSteerDeg:  p.PeakSteerDeg,
			Corrections:   p.Corrections,
			ABSCount:      p.ABSCount,
			Lockups:       p.LockupSamples,
			Wheelspin:     p.WheelspinSamples,
			CoastSeconds:  float32(p.CoastSamples) / 60.0,
			SampleCount:   p.SampleCount,
		})
	}

	if len(in.pbPhases) > 0 {
		lookup := pb.PhaseLookup(in.pbPhases)
		for _, p := range phases {
			if p.SampleCount == 0 {
				continue
			}
			ref, ok := lookup[pb.PhaseKey(p.SegName, string(p.Kind))]
			if !ok {
				continue
			}
			out.VsPB = append(out.VsPB, jsonPhaseDelta{
				Segment:        p.SegName,
				Phase:          string(p.Kind),
				DSpeedEntryKPH: p.SpeedEntryKPH - ref.SpeedEntryKPH,
				DSpeedExitKPH:  p.SpeedExitKPH - ref.SpeedExitKPH,
				DBrakePct:      p.BrakePct - ref.BrakePct,
				DPeakBrakePct:  p.PeakBrakePct - ref.PeakBrakePct,
				DThrottlePct:   p.ThrottlePct - ref.ThrottlePct,
				DLatGAvg:       p.LatGAvg - ref.LatGAvg,
				DCorrections:   p.Corrections - ref.Corrections,
				DABSCount:      p.ABSCount - ref.ABSCount,
				DLockups:       p.LockupSamples - ref.LockupSamples,
				DWheelspin:     p.WheelspinSamples - ref.WheelspinSamples,
				DCoastSeconds:  float32(p.CoastSamples-ref.CoastSamples) / 60.0,
			})
		}
	}

	for _, imp := range analysis.ComputeExitImpact(in.segs, phases) {
		out.ExitImpact = append(out.ExitImpact, jsonExitImpact{
			Corner:               imp.CornerName,
			CornerExitSpeedKPH:   imp.CornerExitSpeedKPH,
			Straight:             imp.StraightName,
			StraightPeakSpeedKPH: imp.StraightPeakSpeedKPH,
		})
	}

	return out
}

// jsonCornerFrom converts a corner's tyre state, turning the stored
// "fraction remaining" wear into the "percent worn" the tables show.
func jsonCornerFrom(c analysis.CornerTyres) jsonCorner {
	return jsonCorner{
		TempInnerC:  c.TempInner,
		TempMidC:    c.TempMid,
		TempOuterC:  c.TempOuter,
		WornInner:   (1 - c.WearInner) * 100,
		WornMid:     (1 - c.WearMid) * 100,
		WornOuter:   (1 - c.WearOuter) * 100,
		PressureKPa: c.PressureKPa,
	}
}

// buildJSONSectors mirrors printSectorTable: per-lap sector times for every
// flying lap, the best time in each sector, and the theoretical best.
func buildJSONSectors(laps []analysis.Lap, sectors []analysis.Sector) *jsonSectors {
	if len(sectors) == 0 {
		return nil
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
		return nil
	}

	out := &jsonSectors{}
	for _, s := range sectors {
		out.StartPct = append(out.StartPct, s.StartPct)
	}

	for ri, st := range rows {
		row := jsonSectorLap{Lap: nums[ri], LapTime: lapTimes[ri]}
		for i := range sectors {
			if i >= len(st) || !st[i].Complete {
				row.Times = append(row.Times, nil)
				continue
			}
			secs := st[i].Seconds
			row.Times = append(row.Times, &secs)
		}
		out.PerLap = append(out.PerLap, row)
	}

	best, from := analysis.BestSectorTimes(rows, nums)
	var theoretical float32
	complete := true
	for i := range sectors {
		if i >= len(best) || !best[i].Complete {
			complete = false
			continue
		}
		out.Best = append(out.Best, best[i].Seconds)
		out.BestFromLap = append(out.BestFromLap, from[i])
		theoretical += best[i].Seconds
	}
	if complete {
		out.Theoretical = &theoretical
	}

	return out
}
