package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
