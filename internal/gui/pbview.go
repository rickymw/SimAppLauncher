package gui

import (
	"net/http"
	"sort"
	"strings"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
)

// pbEntry is one stored personal best as the browser sees it.
//
// The setup arrives as flattened path/value pairs rather than the raw YAML
// block: the page renders it as a table, and doing the flattening here reuses
// analysis.FlattenSetup — the same parse `pb show` and `pb diff` run, so a
// setup value shown in the browser is the one the CLI would print.
type pbEntry struct {
	Key              string       `json:"key"`
	Car              string       `json:"car"`
	Track            string       `json:"track"`
	LapTime          float32      `json:"lapTime"`
	LapTimeFormatted string       `json:"lapTimeFormatted"`
	Date             string       `json:"date,omitempty"`
	Weather          string       `json:"weather,omitempty"`
	Phases           []pb.PBPhase `json:"phases,omitempty"`
	Setup            []setupField `json:"setup,omitempty"`
	BrakeEntries     []brakeEntry `json:"brakeEntries,omitempty"`
	// HasSetup / HasPhases let the list view show which payloads an entry
	// carries without the response having to ship every entry's full setup —
	// the list is served without them and the detail view fills them in.
	HasSetup   bool `json:"hasSetup"`
	HasPhases  bool `json:"hasPhases"`
	BrakePoint int  `json:"brakePointCount"`
}

type setupField struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

type brakeEntry struct {
	Segment  string  `json:"segment"`
	Pct      float32 `json:"pct"`
	LapsUsed int     `json:"lapsUsed"`
}

type pbResponse struct {
	Path    string    `json:"path"`
	Entries []pbEntry `json:"entries"`
}

// handlePBList serves every stored personal best. A `key` parameter asks for
// one entry with its setup and phase data attached; without it the response is
// the summary list, because pb.json here is ~90 KB and shipping every setup to
// draw a five-column table would be most of that for nothing.
func (s *Server) handlePBList(w http.ResponseWriter, r *http.Request) {
	file, err := pb.Load(s.deps.PBPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot read pb.json: "+err.Error())
		return
	}

	want := strings.TrimSpace(r.URL.Query().Get("key"))

	entries := make([]pbEntry, 0, len(file))
	for key, rec := range file {
		if rec == nil {
			continue
		}
		if want != "" && key != want {
			continue
		}
		entries = append(entries, buildPBEntry(key, rec, want != ""))
	}

	// Sorted by track then car. Go map order is random, so an unsorted listing
	// would reshuffle on every reload — the same reason `pb list` sorts.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Track != entries[j].Track {
			return entries[i].Track < entries[j].Track
		}
		return entries[i].Car < entries[j].Car
	})

	if want != "" && len(entries) == 0 {
		writeErr(w, http.StatusNotFound, "no stored personal best for "+want)
		return
	}

	writeJSON(w, http.StatusOK, pbResponse{Path: s.deps.PBPath, Entries: entries})
}

// buildPBEntry converts a stored record. detail controls whether the heavy
// payloads (setup, phases, brake points) are included.
func buildPBEntry(key string, rec *pb.PersonalBest, detail bool) pbEntry {
	e := pbEntry{
		Key:              key,
		Car:              rec.Car,
		Track:            rec.Track,
		LapTime:          rec.LapTime,
		LapTimeFormatted: rec.LapTimeFormatted,
		Date:             rec.Date,
		Weather:          rec.Weather,
		HasSetup:         strings.TrimSpace(rec.Setup) != "",
		HasPhases:        len(rec.Phases) > 0,
		BrakePoint:       len(rec.BrakeEntries),
	}
	if !detail {
		return e
	}

	e.Phases = rec.Phases
	if e.HasSetup {
		// Left in document order, not sorted: FlattenSetup preserves the order
		// the garage screen uses, which is the order someone comparing the page
		// against the sim will be reading in.
		for _, v := range analysis.FlattenSetup(analysis.ParseCarSetupTree(rec.Setup)) {
			e.Setup = append(e.Setup, setupField{Path: v.Path, Value: v.Value})
		}
	}
	if len(rec.BrakeEntries) > 0 {
		segs := make([]string, 0, len(rec.BrakeEntries))
		for seg := range rec.BrakeEntries {
			segs = append(segs, seg)
		}
		sort.Strings(segs)
		e.BrakeEntries = make([]brakeEntry, 0, len(segs))
		for _, seg := range segs {
			be := rec.BrakeEntries[seg]
			e.BrakeEntries = append(e.BrakeEntries, brakeEntry{Segment: seg, Pct: be.Pct, LapsUsed: be.LapsUsed})
		}
	}
	return e
}
