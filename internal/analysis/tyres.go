package analysis

// CornerTyres holds tyre metrics for one wheel corner.
// Inner/Outer are relative to the car (inner = toward centre, outer = away).
// iRacing's tempL/tempM/tempR (left/middle/right tread bands) are mapped accordingly:
// left-side tyres (LF, LR): tempL→Outer, tempR→Inner;
// right-side tyres (RF, RR): tempL→Inner, tempR→Outer.
// Surface (tread) temperature is used rather than iRacing's carcass-temp channels
// (*tempCL/CM/CR), which were found to freeze at a stale cold value for an entire
// session on some cars and only update once at session end — useless for a
// per-lap average. Surface temp updates every sample and tracks real driving load.
type CornerTyres struct {
	// Average surface (tread) temperatures over the lap (°C): left/mid/right tread bands.
	TempInner, TempMid, TempOuter float32

	// End-of-lap wear per tread band (0.0–1.0; 1.0 = new). Subtract from 1.0 for % worn.
	WearInner, WearMid, WearOuter float32

	// Average hot tyre pressure over the lap (kPa).
	PressureKPa float32
}

// TyreSummary holds per-corner tyre state and brake bias for a single lap.
type TyreSummary struct {
	LF, RF, LR, RR CornerTyres

	// Average brake bias over the lap (percentage, e.g. 51.5).
	BrakeBias float32
}

// ComputeTyreSummary computes tyre temperatures and pressures (averaged over all
// samples) and wear (taken from the final sample) for the given lap.
// Returns a zero TyreSummary if the lap has no samples.
func ComputeTyreSummary(lap *Lap) TyreSummary {
	n := len(lap.Samples)
	if n == 0 {
		return TyreSummary{}
	}

	var sumLFtempL, sumLFtempM, sumLFtempR float64
	var sumRFtempL, sumRFtempM, sumRFtempR float64
	var sumLRtempL, sumLRtempM, sumLRtempR float64
	var sumRRtempL, sumRRtempM, sumRRtempR float64

	var sumLFpress, sumRFpress, sumLRpress, sumRRpress float64
	var sumBrakeBias float64

	for _, s := range lap.Samples {
		sumLFtempL += float64(s.LFtempL)
		sumLFtempM += float64(s.LFtempM)
		sumLFtempR += float64(s.LFtempR)
		sumRFtempL += float64(s.RFtempL)
		sumRFtempM += float64(s.RFtempM)
		sumRFtempR += float64(s.RFtempR)
		sumLRtempL += float64(s.LRtempL)
		sumLRtempM += float64(s.LRtempM)
		sumLRtempR += float64(s.LRtempR)
		sumRRtempL += float64(s.RRtempL)
		sumRRtempM += float64(s.RRtempM)
		sumRRtempR += float64(s.RRtempR)

		sumLFpress += float64(s.LFpressure)
		sumRFpress += float64(s.RFpressure)
		sumLRpress += float64(s.LRpressure)
		sumRRpress += float64(s.RRpressure)

		sumBrakeBias += float64(s.BrakeBias)
	}

	fn := float64(n)
	last := lap.Samples[n-1]

	// iRacing tempL/tempM/tempR = left/middle/right across the tread width.
	// For left-side tyres (LF, LR): tempL = outer, tempR = inner.
	// For right-side tyres (RF, RR): tempL = inner, tempR = outer.
	return TyreSummary{
		LF: CornerTyres{
			TempOuter:   float32(sumLFtempL / fn), // tempL = outer for left-side
			TempMid:     float32(sumLFtempM / fn),
			TempInner:   float32(sumLFtempR / fn), // tempR = inner for left-side
			WearOuter:   last.LFwearL,
			WearMid:     last.LFwearM,
			WearInner:   last.LFwearR,
			PressureKPa: float32(sumLFpress / fn),
		},
		RF: CornerTyres{
			TempInner:   float32(sumRFtempL / fn), // tempL = inner for right-side
			TempMid:     float32(sumRFtempM / fn),
			TempOuter:   float32(sumRFtempR / fn), // tempR = outer for right-side
			WearInner:   last.RFwearL,
			WearMid:     last.RFwearM,
			WearOuter:   last.RFwearR,
			PressureKPa: float32(sumRFpress / fn),
		},
		LR: CornerTyres{
			TempOuter:   float32(sumLRtempL / fn), // tempL = outer for left-side
			TempMid:     float32(sumLRtempM / fn),
			TempInner:   float32(sumLRtempR / fn), // tempR = inner for left-side
			WearOuter:   last.LRwearL,
			WearMid:     last.LRwearM,
			WearInner:   last.LRwearR,
			PressureKPa: float32(sumLRpress / fn),
		},
		RR: CornerTyres{
			TempInner:   float32(sumRRtempL / fn), // tempL = inner for right-side
			TempMid:     float32(sumRRtempM / fn),
			TempOuter:   float32(sumRRtempR / fn), // tempR = outer for right-side
			WearInner:   last.RRwearL,
			WearMid:     last.RRwearM,
			WearOuter:   last.RRwearR,
			PressureKPa: float32(sumRRpress / fn),
		},
		BrakeBias: float32(sumBrakeBias / fn),
	}
}
