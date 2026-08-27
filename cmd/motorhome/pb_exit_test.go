package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/analysis"
	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/pb"
)

// The "refuses to guess" behaviours all end in os.Exit, which cannot be
// asserted in-process. The standard workaround is to re-exec the test binary:
// the child runs one named case and exits for real, and the parent inspects the
// exit code and stderr.
//
// These paths are worth the machinery because they are the safety contract for
// a destructive command — a prune that silently selected everything, or a show
// that picked an arbitrary entry, would be a data-loss bug rather than a
// cosmetic one.

const exitCaseEnv = "MOTORHOME_EXIT_CASE"

func TestMain(m *testing.M) {
	switch os.Getenv(exitCaseEnv) {
	case "":
		os.Exit(m.Run())
	case "prune-no-criteria":
		runPBPrune(nil, writeExitCasePBFile())
	case "prune-no-pb-file":
		runPBPrune([]string{"-older-than", "1"}, filepath.Join(os.TempDir(), "motorhome-absent-pb.json"))
	case "show-ambiguous":
		runPBShow([]string{"porsche"}, writeExitCasePBFile())
	case "show-no-match":
		runPBShow([]string{"ferrari"}, writeExitCasePBFile())
	case "unknown-subcommand":
		RunPB([]string{"bogus"}, config.Config{}, writeExitCasePBFile())
	case "coach-trace-bad-segment":
		// The real wiring: coach sets invokedAs before the shared pipeline runs,
		// so the segment error must name coach and -segment, not analyze/-trace.
		invokedAs = coachInvocation
		buildSegmentTraces("T99", traceTestSegs(), exitCaseTraceLaps(), exitCaseTraceLaps(), 0, 0)
	case "coach-trace-no-map":
		invokedAs = coachInvocation
		buildSegmentTraces("T1", nil, exitCaseTraceLaps(), exitCaseTraceLaps(), 0, 0)
	case "analyze-trace-no-map":
		buildSegmentTraces("T1", nil, exitCaseTraceLaps(), exitCaseTraceLaps(), 0, 0)

	// The GUI's subcommandRunner cases. They are here rather than in their own
	// TestMain because a package gets only one, and they need the same re-exec
	// machinery: what is under test is the argv the child receives and the
	// streams the parent gets back.
	case "gui-echo-argv":
		fmt.Println(strings.Join(os.Args[1:], " "))
	case "gui-echo-stderr":
		fmt.Fprintln(os.Stderr, "analyze: lap 99 not found")
		os.Exit(1)
	case "gui-hang":
		// Long enough that the parent's timeout is what ends this, but bounded
		// so a broken timeout leaves a process that still exits on its own.
		time.Sleep(30 * time.Second)
	}
	// A case that returns instead of exiting is itself a failure; make that
	// visible rather than reporting a misleading exit code 0.
	os.Exit(0)
}

// writeExitCasePBFile seeds a two-Porsche store in a temp file for the child
// process. It cannot use t.TempDir() because there is no *testing.T in TestMain.
func writeExitCasePBFile() string {
	dir, err := os.MkdirTemp("", "motorhome-exitcase")
	if err != nil {
		panic(err)
	}
	path := filepath.Join(dir, "pb.json")
	pbf := pb.File{
		pb.Key("Porsche 718 GT4", "Watkins Glen"): {
			Car: "Porsche 718 GT4", Track: "Watkins Glen",
			LapTime: 114.7, LapTimeFormatted: "1:54.700", Date: "2020-01-01",
		},
		pb.Key("Porsche 718 GT4", "Sebring"): {
			Car: "Porsche 718 GT4", Track: "Sebring",
			LapTime: 131.3, LapTimeFormatted: "2:11.300", Date: "2020-01-01",
		},
	}
	if err := pb.Save(path, pbf); err != nil {
		panic(err)
	}
	return path
}

// exitCaseTraceLaps builds the minimal lap set the trace cases need. Like
// writeExitCasePBFile it runs without a *testing.T, in the child process.
func exitCaseTraceLaps() []analysis.Lap {
	return []analysis.Lap{traceTestLap(3, 50)}
}

// runExitCase re-executes this test binary for one named case and returns its
// exit code and stderr.
func runExitCase(t *testing.T, name string) (code int, stderr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), exitCaseEnv+"="+name)

	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()

	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), errBuf.String()
	}
	if err != nil {
		t.Fatalf("running exit case %q: %v", name, err)
	}
	return 0, errBuf.String()
}

// Prune with no criteria must refuse rather than select every entry.
func TestPrune_NoCriteriaExitsNonZero(t *testing.T) {
	code, stderr := runExitCase(t, "prune-no-criteria")

	if code == 0 {
		t.Errorf("prune with no criteria should exit non-zero, got %d", code)
	}
	if !strings.Contains(stderr, "refusing to select every entry") {
		t.Errorf("expected an explanation on stderr, got:\n%s", stderr)
	}
}

func TestPrune_MissingStoreExitsNonZero(t *testing.T) {
	code, stderr := runExitCase(t, "prune-no-pb-file")

	if code == 0 {
		t.Errorf("prune against a missing store should exit non-zero, got %d", code)
	}
	if !strings.Contains(stderr, "no PB entries") {
		t.Errorf("expected a missing-store message, got:\n%s", stderr)
	}
}

// A filter matching several entries must list the candidates rather than
// picking one arbitrarily.
func TestShow_AmbiguousFilterExitsNonZero(t *testing.T) {
	code, stderr := runExitCase(t, "show-ambiguous")

	if code == 0 {
		t.Errorf("an ambiguous filter should exit non-zero, got %d", code)
	}
	if !strings.Contains(stderr, "matches 2 entries") {
		t.Errorf("expected a match count, got:\n%s", stderr)
	}
	// Both candidates have to be named so the user can narrow the filter.
	for _, want := range []string{"Watkins Glen", "Sebring"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("candidate %q not listed:\n%s", want, stderr)
		}
	}
}

func TestShow_NoMatchListsAvailable(t *testing.T) {
	code, stderr := runExitCase(t, "show-no-match")

	if code == 0 {
		t.Errorf("a non-matching filter should exit non-zero, got %d", code)
	}
	if !strings.Contains(stderr, "no entry matches") {
		t.Errorf("expected a no-match message, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Available") {
		t.Errorf("expected the available entries to be listed:\n%s", stderr)
	}
}

func TestRunPB_UnknownSubcommandExitsNonZero(t *testing.T) {
	code, stderr := runExitCase(t, "unknown-subcommand")

	if code == 0 {
		t.Errorf("an unknown subcommand should exit non-zero, got %d", code)
	}
	if !strings.Contains(stderr, "pb <list|show|diff|prune>") {
		t.Errorf("expected usage on stderr, got:\n%s", stderr)
	}
}

// coach runs the analyze pipeline in-process, so a failure inside it must still
// name the command and flag the user typed. Reporting `analyze: ... -trace ...`
// to someone who typed `coach -segment` sends them looking for a flag that is
// not on the command they ran.
func TestCoach_TraceErrorsNameTheCoachInvocation(t *testing.T) {
	code, stderr := runExitCase(t, "coach-trace-bad-segment")

	if code == 0 {
		t.Errorf("an unknown segment should exit non-zero, got %d", code)
	}
	if !strings.HasPrefix(stderr, "coach: ") {
		t.Errorf("expected a coach: prefix, got:\n%s", stderr)
	}
	if strings.Contains(stderr, "analyze:") {
		t.Errorf("error blames analyze, which the user did not run:\n%s", stderr)
	}
	// The available segments are what let the user correct the typo.
	if !strings.Contains(stderr, "T1") {
		t.Errorf("expected the available segments to be listed:\n%s", stderr)
	}
}

func TestCoach_TraceNoMapNamesSegmentFlag(t *testing.T) {
	code, stderr := runExitCase(t, "coach-trace-no-map")

	if code == 0 {
		t.Errorf("tracing without a track map should exit non-zero, got %d", code)
	}
	if !strings.Contains(stderr, "coach: -segment requires a track map") {
		t.Errorf("expected the message to cite -segment under coach, got:\n%s", stderr)
	}
}

// The same path under analyze must still say analyze and -trace — the context is
// swapped by coach, not permanently changed.
func TestAnalyze_TraceNoMapNamesTraceFlag(t *testing.T) {
	code, stderr := runExitCase(t, "analyze-trace-no-map")

	if code == 0 {
		t.Errorf("tracing without a track map should exit non-zero, got %d", code)
	}
	if !strings.Contains(stderr, "analyze: -trace requires a track map") {
		t.Errorf("expected the message to cite -trace under analyze, got:\n%s", stderr)
	}
}

func TestDieMessage(t *testing.T) {
	if got := dieMessage("coach", "segment %q not found", "T99"); got != "coach: segment \"T99\" not found\n" {
		t.Errorf("dieMessage = %q", got)
	}
	if got := dieMessage("analyze", "no samples found in file"); got != "analyze: no samples found in file\n" {
		t.Errorf("dieMessage = %q", got)
	}
}

// The default must stay analyze: every other subcommand shares these paths.
func TestInvokedAsDefaultsToAnalyze(t *testing.T) {
	if invokedAs.cmd != "analyze" || invokedAs.traceFlag != "-trace" {
		t.Errorf("invokedAs = %+v, want the analyze invocation", invokedAs)
	}
}
