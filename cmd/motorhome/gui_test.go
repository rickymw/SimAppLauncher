package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

// subcommandRunner is what the GUI's analyze and usb panels go through, so the
// three things it must get right — threading -config to the child, folding
// stderr into the returned output, and not hanging forever — are worth pinning
// down. These re-exec the test binary through the same TestMain dispatch
// pb_exit_test.go uses; what is under test is the plumbing, not what analyze
// does once it receives the arguments.

func guiChildExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("cannot resolve the test binary: %v", err)
	}
	return exe
}

func TestSubcommandRunnerThreadsConfigPath(t *testing.T) {
	t.Setenv(exitCaseEnv, "gui-echo-argv")
	run := subcommandRunner(guiChildExe(t), `C:\rig\launcher.config.json`)

	out, err := run(30*time.Second, "analyze", "-json", "session.ibt")
	if err != nil {
		t.Fatalf("run: %v (%s)", err, out)
	}

	// -config must come first and must name the server's own config. An
	// analysis that quietly used the child's default would read a different
	// ibtDir and write a different pb.json than the interface is showing.
	got := strings.TrimSpace(string(out))
	want := `-config C:\rig\launcher.config.json analyze -json session.ibt`
	if got != want {
		t.Errorf("child argv =\n  %s\nwant\n  %s", got, want)
	}
}

func TestSubcommandRunnerCapturesStderr(t *testing.T) {
	t.Setenv(exitCaseEnv, "gui-echo-stderr")
	run := subcommandRunner(guiChildExe(t), "cfg.json")

	out, err := run(30*time.Second, "analyze")

	if err == nil {
		t.Error("a child that exited non-zero was reported as successful")
	}
	// analyze writes its one-line reason to stderr, and that message is the
	// only thing that tells the user what actually went wrong.
	if !strings.Contains(string(out), "analyze: lap 99 not found") {
		t.Errorf("output = %q, want the child's stderr folded in", out)
	}
}

func TestSubcommandRunnerTimesOut(t *testing.T) {
	t.Setenv(exitCaseEnv, "gui-hang")
	run := subcommandRunner(guiChildExe(t), "cfg.json")

	start := time.Now()
	_, err := run(500*time.Millisecond, "analyze")

	if err == nil {
		t.Fatal("a hung child was reported as successful")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want a timeout that names the subcommand", err)
	}
	// The point of the timeout is that the request does not hang with it.
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("run returned after %s — the deadline did not kill the child", elapsed)
	}
}
