package analysis

// CornerTyres holds tyre metrics for one wheel corner.
// Inner/Outer are relative to the car (inner = toward centre, outer = away).
// iRacing's CL/CM/CR (left/middle/right tread bands) are mapped accordingly:
// left-side tyres (LF, LR): CL→Outer, CR→Inner;
// right-side tyres (RF, RR): CL→Inner, CR→Outer.
type CornerTyres struct {
	// Average carcass temperatures over the lap (°C): CL, CM, CR tread bands.
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
		sumLFtempL += float64(s.LFtempCL)
		sumLFtempM += float64(s.LFtempCM)
		sumLFtempR += float64(s.LFtempCR)
		sumRFtempL += float64(s.RFtempCL)
		sumRFtempM += float64(s.RFtempCM)
		sumRFtempR += float64(s.RFtempCR)
		sumLRtempL += float64(s.LRtempCL)
		sumLRtempM += float64(s.LRtempCM)
		sumLRtempR += float64(s.LRtempCR)
		sumRRtempL += float64(s.RRtempCL)
		sumRRtempM += float64(s.RRtempCM)
		sumRRtempR += float64(s.RRtempCR)

		sumLFpress += float64(s.LFpressure)
		sumRFpress += float64(s.RFpressure)
		sumLRpress += float64(s.LRpressure)
		sumRRpress += float64(s.RRpressure)

		sumBrakeBias += float64(s.BrakeBias)
	}

	fn := float64(n)
	last := lap.Samples[n-1]

	// iRacing CL/CM/CR = left/middle/right across the tread width.
	// For left-side tyres (LF, LR): CL = outer, CR = inner.
	// For right-side tyres (RF, RR): CL = inner, CR = outer.
	return TyreSummary{
		LF: CornerTyres{
			TempOuter: float32(sumLFtempL / fn), // CL = outer for left-side
			TempMid:   float32(sumLFtempM / fn),
			TempInner: float32(sumLFtempR / fn), // CR = inner for left-side
			WearOuter: last.LFwearL,
			WearMid:   last.LFwearM,
			WearInner: last.LFwearR,
			PressureKPa: float32(sumLFpress / fn),
		},
		RF: CornerTyres{
			TempInner: float32(sumRFtempL / fn), // CL = inner for right-side
			TempMid:   float32(sumRFtempM / fn),
			TempOuter: float32(sumRFtempR / fn), // CR = outer for right-side
			WearInner: last.RFwearL,
			WearMid:   last.RFwearM,
			WearOuter: last.RFwearR,
			PressureKPa: float32(sumRFpress / fn),
		},
		LR: CornerTyres{
			TempOuter: float32(sumLRtempL / fn), // CL = outer for left-side
			TempMid:   float32(sumLRtempM / fn),
			TempInner: float32(sumLRtempR / fn), // CR = inner for left-side
			WearOuter: last.LRwearL,
			WearMid:   last.LRwearM,
			WearInner: last.LRwearR,
			PressureKPa: float32(sumLRpress / fn),
		},
		RR: CornerTyres{
			TempInner: float32(sumRRtempL / fn), // CL = inner for right-side
			TempMid:   float32(sumRRtempM / fn),
			TempOuter: float32(sumRRtempR / fn), // CR = outer for right-side
			WearInner: last.RRwearL,
			WearMid:   last.RRwearM,
			WearOuter: last.RRwearR,
			PressureKPa: float32(sumRRpress / fn),
		},
		BrakeBias: float32(sumBrakeBias / fn),
	}
}
