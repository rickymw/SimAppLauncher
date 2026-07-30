package main

// Voice notes in the analyze output: finding the notes file recorded alongside
// an .ibt, and rendering the notes against the lap and corner they were spoken
// at.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/notes"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// notesFileForIbt returns the path the notes subcommand would have written for
// a session recorded alongside ibtPath.
//
// The notes subcommand names each session file after the .ibt it detected
// (resolveSessionPath in notes.go), so the join is by filename. Returns "" when
// no notes directory is configured.
func notesFileForIbt(notesDir, ibtPath string) string {
	if notesDir == "" || ibtPath == "" {
		return ""
	}
	base := filepath.Base(ibtPath)
	// Trim the extension case-insensitively: Windows filesystems are, so a
	// "SESSION.IBT" must resolve to the same notes file as "session.ibt".
	if ext := filepath.Ext(base); strings.EqualFold(ext, ".ibt") {
		base = base[:len(base)-len(ext)]
	}
	return filepath.Join(notesDir, base+".json")
}

// loadNotesForIbt loads the voice notes recorded during the session in ibtPath.
//
// A missing notes file is the normal case — most sessions have no notes — so it
// returns no notes and no error. Only a file that exists but cannot be read or
// parsed produces an error.
func loadNotesForIbt(notesDir, ibtPath string) (sess notes.Session, path string, err error) {
	path = notesFileForIbt(notesDir, ibtPath)
	if path == "" {
		return notes.Session{}, "", nil
	}
	if _, statErr := os.Stat(path); statErr != nil {
		return notes.Session{}, "", nil
	}
	sess, err = notes.LoadSession(path)
	return sess, path, err
}

// locateSessionNotes resolves a notes session onto the telemetry. Returns nil
// when there are no notes to place.
func locateSessionNotes(sess notes.Session, laps []analysis.Lap, segs []trackmap.Segment, recStart time.Time, lag time.Duration) []analysis.LocatedNote {
	if len(sess.Notes) == 0 {
		return nil
	}
	in := make([]analysis.NoteInput, 0, len(sess.Notes))
	for _, n := range sess.Notes {
		in = append(in, analysis.NoteInput{At: n.Anchor(), Text: n.Text})
	}
	return analysis.LocateNotes(laps, segs, recStart, lag, in)
}

// printNotes renders located voice notes as a table of lap, position and text.
//
// Notes that could not be placed (spoken before the recording started or after
// it stopped) are still printed, with dashes for the position columns — the
// text is the point of the note, and silently dropping it would be worse than
// admitting the position is unknown.
func printNotes(located []analysis.LocatedNote, sourceFile string) {
	if len(located) == 0 {
		return
	}

	whereW := 5 // minimum "Where"
	for _, n := range located {
		if len(n.SegName) > whereW {
			whereW = len(n.SegName)
		}
	}

	label := fmt.Sprintf("Notes (%d)", len(located))
	if sourceFile != "" {
		label = fmt.Sprintf("Notes (%d from %s)", len(located), filepath.Base(sourceFile))
	}
	aprintf("%s:\n\n", label)
	aprintf("  %3s | %-*s | %4s | %s\n", "Lap", whereW, "Where", "Lap%", "Note")
	aprintf("  %3s-|-%s-|-%s-|-%s\n", "---", dashes(whereW), "----", dashes(4))

	unlocated := 0
	for _, n := range located {
		if !n.Located {
			unlocated++
			aprintf("  %3s | %-*s | %4s | %s\n", "-", whereW, "-", "-", n.Text)
			continue
		}
		where := n.SegName
		if where == "" {
			where = "-"
		}
		aprintf("  %3d | %-*s | %3.0f%% | %s\n",
			n.LapNumber, whereW, where, n.LapDistPct*100, n.Text)
	}
	aprintln()

	if unlocated > 0 {
		aprintf("  %d %s outside the telemetry recording — no track position.\n",
			unlocated, pluralize(unlocated, "note falls", "notes fall"))
		aprintln()
	}
}
