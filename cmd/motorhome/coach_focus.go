package main

// `coach -segment`: narrowing the brief to named corners.
//
// The aggregate rows the brief normally carries answer "which corner is costing
// time". They cannot answer "what exactly is happening in it" — a mean and a
// standard deviation describe a corner, they do not replay it. Focusing swaps
// breadth for depth: the per-segment rows for everything else come out, and the
// segment's actual samples (see analyze_trace.go) go in.
//
// The trade is only safe if the reader knows it was made. A narrowed brief looks
// exactly like a whole-session one, so anything that drops rows here must also
// set Focus — writeCoachOrientation turns that into a line the reader cannot
// miss. Without it the assistant would confidently report the session's main
// problem having been shown one corner of it.

import (
	"fmt"
	"strings"

	"github.com/rickymw/MotorHome/internal/analysis"
)

// focusOnSegments narrows res to the segments named in spec, returning the
// narrowed document and the canonical segment names it was narrowed to.
//
// Only per-segment collections are filtered. Session-level content — the lap
// list, sector times, fuel, the PB header, voice notes — is left whole: it is
// small, it is the context that makes a single corner interpretable, and a
// sector time is the one thing that says whether the focused corner is where the
// time is actually going.
func focusOnSegments(res analyzeResult, spec string) (analyzeResult, []string, error) {
	if res.TrackMap == nil || len(res.TrackMap.Segments) == 0 {
		return res, nil, fmt.Errorf(
			"-segment needs a track map to name corners against; this session has none " +
				"(run analyze once on a session with 2+ clean laps to detect one)")
	}

	idxs, err := analysis.ResolveSegmentList(res.TrackMap.Segments, spec)
	if err != nil {
		return res, nil, fmt.Errorf("%v — available: %s", err, segmentNames(res.TrackMap.Segments))
	}

	names := make([]string, 0, len(idxs))
	keep := make(map[string]bool, len(idxs))
	for _, i := range idxs {
		n := res.TrackMap.Segments[i].Name
		names = append(names, n)
		keep[strings.ToLower(n)] = true
	}
	kept := func(name string) bool { return keep[strings.ToLower(name)] }

	if res.AnalysedLap != nil {
		al := *res.AnalysedLap

		phases := al.Phases[:0:0]
		for _, p := range al.Phases {
			if kept(p.Segment) {
				phases = append(phases, p)
			}
		}
		al.Phases = phases

		vsPB := al.VsPB[:0:0]
		for _, d := range al.VsPB {
			if kept(d.Segment) {
				vsPB = append(vsPB, d)
			}
		}
		al.VsPB = vsPB

		// Exit impact is kept on the focused *corner*, not the straight it feeds.
		// The straight is only there to say what the corner's exit was worth, so
		// a row naming a corner nobody asked about carries nothing.
		impact := al.ExitImpact[:0:0]
		for _, e := range al.ExitImpact {
			if kept(e.Corner) {
				impact = append(impact, e)
			}
		}
		al.ExitImpact = impact

		res.AnalysedLap = &al
	}

	cons := res.Consistency[:0:0]
	for _, c := range res.Consistency {
		if kept(c.Segment) {
			cons = append(cons, c)
		}
	}
	res.Consistency = cons

	res.Focus = names
	return res, names, nil
}
