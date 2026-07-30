package main

// Machine-readable analyze output (-json).
//
// This is a second renderer over the same computed values the tables use, not a
// second analysis: buildAnalyzeResult takes what RunAnalyze already produced and
// reshapes it. The point is that the AI coaching workflow (and anything else
// downstream) does not have to parse fixed-width ASCII tables whose column
// layout is free to change.
//
// The types here are deliberately separate from the internal analysis structs.
// Those are free to be reordered or renamed as internals; this is a published
// wire format, and pinning it to its own types is what lets the two move
// independently. Schema changes should bump analyzeSchema.

import (
	"encoding/json"
	"io"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// analyzeSchema identifies the shape of the JSON document. Bump the minor part
// for additive changes, the major part when a consumer would break.
const analyzeSchema = "motorhome.analyze/1.0"

type analyzeResult struct {
	Schema      string `json:"schema"`
	File        string `json:"file"`
	Driver      string `json:"driver,omitempty"`
	Car         string `json:"car,omitempty"`
	Track       string `json:"track,omitempty"`
	SessionDate string `json:"sessionDate"`
	Samples     int    `json:"samples"`
	TickRateHz  int    `json:"tickRateHz"`

	TrackMap *jsonTrackMap `json:"trackMap,omitempty"`
	PB       *jsonPB       `json:"pb,omitempty"`

	Laps    []jsonLap    `json:"laps"`
	Sectors *jsonSectors `json:"sectors,omitempty"`

	AnalysedLap *jsonAnalysedLap  `json:"analysedLap,omitempty"`
	Consistency []jsonConsistency `json:"consistency,omitempty"`
	Fuel        *jsonFuel         `json:"fuel,omitempty"`
	Notes       []jsonNote        `json:"notes,omitempty"`
}

type jsonFuel struct {
	StartLitres float32 `json:"startLitres"`
	// EndLitres is measured at the end of the last lap that did not refuel,
	// not at the last sample of the session.
	EndLitres     float32 `json:"endLitres"`
	UsedLitres    float32 `json:"usedLitres"`
	Refuelled     bool    `json:"refuelled,omitempty"`
	LapsMeasured  int     `json:"lapsMeasured"`
	AvgPerLap     float32 `json:"avgPerLapLitres"`
	MedianPerLap  float32 `json:"medianPerLapLitres"`
	WorstPerLap   float32 `json:"worstPerLapLitres"`
	AvgUsePerHour float32 `json:"avgUsePerHourKgH,omitempty"`
	// Two remaining-laps figures: planning a stint on the average runs dry
	// half the time, so the worst-lap figure is the one a stint must survive.
	LapsRemainingAvg   float32       `json:"lapsRemainingAvg"`
	LapsRemainingWorst float32       `json:"lapsRemainingWorst"`
	PerLap             []jsonLapFuel `json:"perLap,omitempty"`
}

type jsonLapFuel struct {
	Lap         int     `json:"lap"`
	StartLitres float32 `json:"startLitres"`
	EndLitres   float32 `json:"endLitres"`
	UsedLitres  float32 `json:"usedLitres"`
	Refuelled   bool    `json:"refuelled,omitempty"`
}

type jsonTrackMap struct {
	Segments []trackmap.Segment `json:"segments,omitempty"`
	// SegmentCount stands in for Segments when the geometry has been dropped
	// (the coach brief does this — see trimForCoaching).
	SegmentCount int    `json:"segmentCount,omitempty"`
	GeoMethod    string `json:"geoMethod,omitempty"`
	Confidence   string `json:"confidence,omitempty"`
	LapsUsed     int    `json:"lapsUsed,omitempty"`
	SessionsUsed int    `json:"sessionsUsed,omitempty"`
	// MatchScore is nil when no comparable lap was available to score the
	// stored map against — distinct from a score of 0, which means the lap
	// matched nothing.
	MatchScore *float32 `json:"matchScore,omitempty"`
}

type jsonPB struct {
	LapTime          float32 `json:"lapTime"`
	LapTimeFormatted string  `json:"lapTimeFormatted"`
	Date             string  `json:"date,omitempty"`
	Weather          string  `json:"weather,omitempty"`
	DeltaToBest      float32 `json:"deltaToBest"` // best lap this session − PB; negative = new PB
}

type jsonLap struct {
	Number        int     `json:"number"`
	Kind          string  `json:"kind"`
	LapTime       float32 `json:"lapTime,omitempty"`
	TimeFormatted string  `json:"timeFormatted,omitempty"`
	Cut           bool    `json:"cut,omitempty"`
	PartialStart  bool    `json:"partialStart,omitempty"`
	Complete      bool    `json:"complete"`
	Comparable    bool    `json:"comparable"` // in the filtered set used for cross-lap views
}

type jsonSectors struct {
	StartPct    []float32       `json:"startPct"`
	PerLap      []jsonSectorLap `json:"perLap"`
	Best        []float32       `json:"best,omitempty"`
	BestFromLap []int           `json:"bestFromLap,omitempty"`
	// Theoretical is the sum of the best sector times, and is nil when at least
	// one sector was never completed — a partial sum would understate the lap.
	Theoretical *float32 `json:"theoretical,omitempty"`
}

type jsonSectorLap struct {
	Lap int `json:"lap"`
	// Times is one entry per sector; nil entries are sectors the lap did not
	// cover completely (an out lap, or a recording that stopped mid-lap).
	Times   []*float32 `json:"times"`
	LapTime float32    `json:"lapTime"`
}

type jsonAnalysedLap struct {
	Number        int              `json:"number"`
	LapTime       float32          `json:"lapTime"`
	TimeFormatted string           `json:"timeFormatted"`
	Selection     string           `json:"selection"` // "best" or "explicit"
	Phases        []jsonPhase      `json:"phases,omitempty"`
	VsPB          []jsonPhaseDelta `json:"vsPB,omitempty"`
	ExitImpact    []jsonExitImpact `json:"exitImpact,omitempty"`
	Tyres         *jsonTyreSummary `json:"tyres,omitempty"`
	// Zones is the fallback 20-zone breakdown, present only when there is no
	// track map and therefore no named segments to phase.
	Zones []analysis.Zone `json:"zones,omitempty"`
}

type jsonPhase struct {
	Segment       string  `json:"segment"`
	SegmentIndex  int     `json:"segmentIndex"`
	Phase         string  `json:"phase"`
	SpeedEntryKPH float32 `json:"speedEntryKph"`
	SpeedExitKPH  float32 `json:"speedExitKph"`
	PeakSpeedKPH  float32 `json:"peakSpeedKph"`
	BrakePct      float32 `json:"brakePct"`
	PeakBrakePct  float32 `json:"peakBrakePct"`
	ThrottlePct   float32 `json:"throttlePct"`
	LatGAvg       float32 `json:"latGAvg"`
	PeakSteerDeg  float32 `json:"peakSteerDeg"`
	Corrections   int     `json:"corrections"`
	ABSCount      int     `json:"absSamples"`
	Lockups       int     `json:"lockupSamples"`
	Wheelspin     int     `json:"wheelspinSamples"`
	CoastSeconds  float32 `json:"coastSeconds"`
	SampleCount   int     `json:"sampleCount"`
}

type jsonPhaseDelta struct {
	Segment        string  `json:"segment"`
	Phase          string  `json:"phase"`
	DSpeedEntryKPH float32 `json:"dSpeedEntryKph"`
	DSpeedExitKPH  float32 `json:"dSpeedExitKph"`
	DBrakePct      float32 `json:"dBrakePct"`
	DPeakBrakePct  float32 `json:"dPeakBrakePct"`
	DThrottlePct   float32 `json:"dThrottlePct"`
	DLatGAvg       float32 `json:"dLatGAvg"`
	DCorrections   int     `json:"dCorrections"`
	DABSCount      int     `json:"dAbsSamples"`
	DLockups       int     `json:"dLockupSamples"`
	DWheelspin     int     `json:"dWheelspinSamples"`
	DCoastSeconds  float32 `json:"dCoastSeconds"`
}

type jsonExitImpact struct {
	Corner               string  `json:"corner"`
	CornerExitSpeedKPH   float32 `json:"cornerExitSpeedKph"`
	Straight             string  `json:"straight"`
	StraightPeakSpeedKPH float32 `json:"straightPeakSpeedKph"`
}

type jsonTyreSummary struct {
	BrakeBias float32               `json:"brakeBias"`
	Corners   map[string]jsonCorner `json:"corners"`
}

type jsonCorner struct {
	TempInnerC  float32 `json:"tempInnerC"`
	TempMidC    float32 `json:"tempMidC"`
	TempOuterC  float32 `json:"tempOuterC"`
	WornInner   float32 `json:"wornInnerPct"`
	WornMid     float32 `json:"wornMidPct"`
	WornOuter   float32 `json:"wornOuterPct"`
	PressureKPa float32 `json:"pressureKpa"`
}

type jsonConsistency struct {
	Segment        string  `json:"segment"`
	Phase          string  `json:"phase"`
	Laps           int     `json:"laps"`
	EntrySpeedMean float32 `json:"entrySpeedMeanKph"`
	EntrySpeedSD   float32 `json:"entrySpeedSdKph"`
	ExitSpeedMean  float32 `json:"exitSpeedMeanKph"`
	ExitSpeedSD    float32 `json:"exitSpeedSdKph"`
	PeakBrakeMean  float32 `json:"peakBrakeMeanPct"`
	PeakBrakeSD    float32 `json:"peakBrakeSdPct"`
	LatGMean       float32 `json:"latGMean"`
	LatGSD         float32 `json:"latGSd"`
	CoastMean      float32 `json:"coastMeanSeconds"`
	CoastSD        float32 `json:"coastSdSeconds"`
	BestExitKPH    float32 `json:"bestExitSpeedKph"`
	BestExitLap    int     `json:"bestExitLap"`
}

type jsonNote struct {
	Text       string  `json:"text"`
	At         string  `json:"at"`
	Located    bool    `json:"located"`
	Lap        int     `json:"lap,omitempty"`
	LapDistPct float32 `json:"lapDistPct,omitempty"`
	Segment    string  `json:"segment,omitempty"`
}

// analyzeResultInput is everything buildAnalyzeResult needs, gathered by
// RunAnalyze as it goes.
type analyzeResultInput struct {
	ibtPath     string
	meta        analysis.SessionMeta
	sessionDate time.Time
	sampleCount int
	tickRate    int

	laps           []analysis.Lap
	comparableLaps []analysis.Lap
	lapNum         int
	segs           []trackmap.Segment
	brakeEntries   pb.BrakeEntryMap
	pbPhases       []pb.PBPhase
	pbEntry        *pb.PersonalBest
	sectors        []analysis.Sector
	consistency    []analysis.ConsistencyRow
	fuel           analysis.FuelSummary
	notes          []analysis.LocatedNote
	notesFile      string

	trackMap   *trackmap.TrackMap
	matchScore float32
	geomConf   trackmap.GeometryConfidence
}

// writeAnalyzeJSON encodes res to w as indented JSON with a trailing newline.
func writeAnalyzeJSON(w io.Writer, res analyzeResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Track/corner names are free text from iRacing and trackref.json; escaping
	// them as < etc. makes the document harder to read for no benefit,
	// since it is never embedded in HTML.
	enc.SetEscapeHTML(false)
	return enc.Encode(res)
}

// writeStoredPBJSON encodes a stored personal best. Used by "-lap pb -json",
// which has no session to describe — the PB record is the whole document.
func writeStoredPBJSON(w io.Writer, entry *pb.PersonalBest) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(entry)
}
