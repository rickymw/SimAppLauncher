// Package pb — phases.go stores per-phase telemetry snapshots alongside PB records.
// When a new personal best is set, the phase data from that lap is saved so that
// future sessions can show per-segment deltas against the PB.
package pb

// PBPhase stores the coaching-relevant statistics for one phase of a PB lap.
// Fields mirror analysis.Phase but are JSON-serialisable and decoupled from
// the analysis package to avoid circular imports.
type PBPhase struct {
	SegName string `json:"segName"`
	Kind    string `json:"kind"` // "entry", "mid", "exit", "full"

	SpeedEntryKPH float32 `json:"speedEntryKPH"`
	SpeedExitKPH  float32 `json:"speedExitKPH"`

	BrakePct     float32 `json:"brakePct"`
	PeakBrakePct float32 `json:"peakBrakePct"`
	ThrottlePct  float32 `json:"throttlePct"`
	LatGAvg      float32 `json:"latGAvg"`

	PeakSteerDeg float32 `json:"peakSteerDeg"`
	Corrections  int     `json:"corrections"`

	ABSCount         int `json:"absCount"`
	LockupSamples    int `json:"lockupSamples"`
	WheelspinSamples int `json:"wheelspinSamples"`
	CoastSamples     int `json:"coastSamples"`
	SampleCount      int `json:"sampleCount"`
}

// PhaseKey returns a lookup key for matching phases across laps: "SegName|Kind".
func PhaseKey(segName, kind string) string {
	return segName + "|" + kind
}

// PhaseLookup builds a map from PhaseKey → *PBPhase for fast lookup.
func PhaseLookup(phases []PBPhase) map[string]*PBPhase {
	m := make(map[string]*PBPhase, len(phases))
	for i := range phases {
		p := &phases[i]
		m[PhaseKey(p.SegName, p.Kind)] = p
	}
	return m
}
