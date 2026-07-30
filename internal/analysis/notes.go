package analysis

// Locating voice notes in telemetry: mapping the wall-clock instant a note was
// spoken onto the lap and track segment being driven at that moment.

import (
	"sort"
	"time"

	"github.com/rickymw/MotorHome/internal/trackmap"
)

// DefaultNoteLag is the assumed delay between an event on track and the driver
// starting to talk about it: recognise it, decide it is worth saying, reach the
// button. Subtracted from a note's anchor time before it is located.
//
// It is an estimate, not a measurement, which is why it is a tunable parameter
// rather than a constant baked into the join. Zero would be a worse default: a
// note about a corner can never be recorded before that corner happened, so an
// uncorrected timestamp is guaranteed to land late — typically in the following
// straight or corner.
const DefaultNoteLag = 2 * time.Second

// NoteInput is one voice note awaiting location.
//
// At should be the moment the driver started speaking, when that is known.
// Notes recorded before the notes package tracked speech start only have the
// end-of-utterance time, which lands later on track by the length of the
// utterance — see LocateNotes.
type NoteInput struct {
	At   time.Time
	Text string
}

// LocatedNote is a note resolved to a position on track.
//
// Located is false when the note's corrected timestamp falls outside the
// telemetry recording — a note made in the pits before the first sample, or
// after the recording stopped. Those still carry their Text; only the position
// fields are meaningless.
type LocatedNote struct {
	Text string
	At   time.Time // the note's own timestamp, uncorrected

	Located       bool
	IntoRecording float64 // seconds from the first telemetry sample
	LapNumber     int
	LapDistPct    float32
	SegIndex      int // -1 when no segment covers the position
	SegName       string
}

// LocateNotes maps each note onto the lap and segment being driven when it was
// spoken, and returns the results sorted by timestamp.
//
// recStart is the wall-clock instant the recording began — the .ibt disk
// header's SessionStartDate. The mapping is anchored on the first telemetry
// sample rather than on the header's SessionStartTime field: the first sample's
// SessionTime is by definition the start of the recording, so anchoring there
// holds regardless of how the header field is interpreted.
//
// lag is subtracted from each note's timestamp before locating it; pass
// DefaultNoteLag for the standard correction, or 0 to locate notes exactly
// where their timestamp falls.
//
// segs may be nil, in which case notes are still resolved to a lap and lap
// distance but carry no segment name.
func LocateNotes(laps []Lap, segs []trackmap.Segment, recStart time.Time, lag time.Duration, in []NoteInput) []LocatedNote {
	out := make([]LocatedNote, 0, len(in))
	for _, n := range in {
		out = append(out, LocatedNote{Text: n.Text, At: n.At, SegIndex: -1})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })

	// Flatten the lap boundaries once: for each lap, the SessionTime of its
	// first and last sample.
	type lapSpan struct {
		lap        *Lap
		start, end float64
	}
	var spans []lapSpan
	for i := range laps {
		l := &laps[i]
		if len(l.Samples) == 0 {
			continue
		}
		spans = append(spans, lapSpan{
			lap:   l,
			start: l.Samples[0].SessionTime,
			end:   l.Samples[len(l.Samples)-1].SessionTime,
		})
	}
	if len(spans) == 0 {
		return out
	}

	t0 := spans[0].start
	tEnd := spans[len(spans)-1].end

	var effEntry, effExit []float32
	if len(segs) > 0 {
		effEntry, effExit = segmentEffBounds(segs)
	}

	for i := range out {
		target := out[i].At.Add(-lag)
		offset := target.Sub(recStart).Seconds()
		st := t0 + offset

		if st < t0 || st > tEnd {
			continue
		}

		// Find the lap covering st. Laps are contiguous and ordered, so a
		// linear scan over lap spans is cheap even for a long session.
		var span *lapSpan
		for j := range spans {
			if st <= spans[j].end {
				span = &spans[j]
				break
			}
		}
		if span == nil {
			continue
		}

		s := nearestSampleByTime(span.lap.Samples, st)
		out[i].Located = true
		out[i].IntoRecording = st - t0
		out[i].LapNumber = span.lap.Number
		out[i].LapDistPct = s.LapDistPct
		if effEntry != nil {
			if idx := segmentForEffPct(s.LapDistPct, effEntry, effExit); idx >= 0 {
				out[i].SegIndex = idx
				out[i].SegName = segs[idx].Name
			}
		}
	}

	return out
}

// nearestSampleByTime returns the sample whose SessionTime is closest to st,
// clamping to the ends. An exact midpoint resolves to the earlier sample.
// samples must be non-empty and ordered by SessionTime.
func nearestSampleByTime(samples []SampleData, st float64) SampleData {
	i := sort.Search(len(samples), func(i int) bool {
		return samples[i].SessionTime >= st
	})
	if i == 0 {
		return samples[0]
	}
	if i >= len(samples) {
		return samples[len(samples)-1]
	}
	if samples[i].SessionTime-st < st-samples[i-1].SessionTime {
		return samples[i]
	}
	return samples[i-1]
}
