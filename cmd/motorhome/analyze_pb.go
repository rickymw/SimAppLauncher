package main

// Stored personal-best rendering for "analyze -lap pb": resolve a PB from
// pb.json and print it without running the full analysis pipeline, plus the
// conversion from computed phases into the PB storage format.

import (
	"fmt"
	"os"
	"sort"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/pb"
)

// runStoredPBNoIBT prints the stored PB when no .ibt file was given on the
// command line. Uses the only entry if pb.json has just one; otherwise lists
// the available entries.
func runStoredPBNoIBT(pbPath string, jsonMode bool) {
	pbf := loadPBOrDie(pbPath)
	if len(pbf) == 1 {
		var entry *pb.PersonalBest
		for _, e := range pbf {
			entry = e
		}
		emitStoredPB(entry, jsonMode)
		return
	}
	fmt.Fprintln(os.Stderr, "Multiple PB entries — pass an .ibt file for the session you want, or specify the car/track context:")
	keys := make([]string, 0, len(pbf))
	for k := range pbf {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		entry := pbf[k]
		fmt.Fprintf(os.Stderr, "  %s — %s on %s\n", entry.LapTimeFormatted, entry.Car, entry.Track)
	}
	os.Exit(1)
}

// runStoredPBForCarTrack prints the PB stored for car+track, resolved from
// the .ibt's session YAML. Errors out if no PB exists for that combination.
func runStoredPBForCarTrack(pbPath, car, track string, jsonMode bool) {
	pbf := loadPBOrDie(pbPath)
	entry := pbf[pb.Key(car, track)]
	if entry == nil {
		analyzeDie("no stored PB for %q on %q — drive a flying lap to set one", car, track)
	}
	emitStoredPB(entry, jsonMode)
}

// emitStoredPB renders a stored PB as tables, or as JSON when -json is set.
// The PB record is already the whole document in that mode — there is no
// session to describe alongside it.
func emitStoredPB(entry *pb.PersonalBest, jsonMode bool) {
	if jsonMode {
		if err := writeStoredPBJSON(os.Stdout, entry); err != nil {
			analyzeDie("writing JSON: %v", err)
		}
		return
	}
	printStoredPB(entry)
}

// loadPBOrDie loads pb.json and exits with a clear message if it is missing
// or empty.
func loadPBOrDie(pbPath string) pb.File {
	if pbPath == "" {
		analyzeDie("no pb.json path configured")
	}
	pbf, err := pb.Load(pbPath)
	if err != nil {
		analyzeDie("loading pb.json: %v", err)
	}
	if len(pbf) == 0 {
		analyzeDie("no PB entries in %s — drive a flying lap first", pbPath)
	}
	return pbf
}

// printStoredPB renders a stored PB entry: car/track header, setup tables (if
// stored), and the phase table from the saved phases. Used by "analyze -lap pb".
func printStoredPB(entry *pb.PersonalBest) {
	aprintf("Car:   %s\n", fallback(entry.Car, "(unknown)"))
	aprintf("Track: %s\n", fallback(entry.Track, "(unknown)"))
	aprintf("PB:    %s — set %s, %s\n\n",
		entry.LapTimeFormatted, fallback(entry.Date, "?"), fallback(entry.Weather, "weather unknown"))

	if entry.Setup != "" {
		if nodes := analysis.ParseCarSetupTree(entry.Setup); nodes != nil {
			printSetupTables(nodes)
		}
	}

	if len(entry.Phases) == 0 {
		aprintln("(no phase data stored for this PB — set a new PB to populate it)")
		return
	}
	printStoredPhaseTable(entry.LapTimeFormatted, entry.Phases)
}

// printStoredPhaseTable mirrors printPhaseTable but works from stored PBPhase
// records (no Lap/Samples context).
func printStoredPhaseTable(lapTimeFormatted string, phases []pb.PBPhase) {
	aprintf("PB lap — %s\n\n", lapTimeFormatted)

	nameW := 4
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

// phasesToPB converts analysis phases to the PB storage format.
func phasesToPB(phases []analysis.Phase) []pb.PBPhase {
	out := make([]pb.PBPhase, len(phases))
	for i, p := range phases {
		out[i] = pb.PBPhase{
			SegName:          p.SegName,
			Kind:             string(p.Kind),
			SpeedEntryKPH:    p.SpeedEntryKPH,
			SpeedExitKPH:     p.SpeedExitKPH,
			BrakePct:         p.BrakePct,
			PeakBrakePct:     p.PeakBrakePct,
			ThrottlePct:      p.ThrottlePct,
			LatGAvg:          p.LatGAvg,
			PeakSteerDeg:     p.PeakSteerDeg,
			Corrections:      p.Corrections,
			ABSCount:         p.ABSCount,
			LockupSamples:    p.LockupSamples,
			WheelspinSamples: p.WheelspinSamples,
			CoastSamples:     p.CoastSamples,
			SampleCount:      p.SampleCount,
		}
	}
	return out
}
