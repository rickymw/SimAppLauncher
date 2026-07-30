package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// captureStdout replaces os.Stdout with a pipe whose output is teed to the
// original stdout and captured into a buffer. The returned finish function
// restores os.Stdout, drains the pipe, and returns the captured content.
//
// os.Exit skips deferred functions, so on analyzeDie error paths the
// captured buffer is lost — which is the desired behaviour: we don't want
// partial broken output copied to the clipboard.
func captureStdout() (finish func() string, err error) {
	return captureStdoutTee(true)
}

// captureStdoutSilent captures os.Stdout without echoing it to the terminal.
// Used by the coach subcommand, which runs the analyze pipeline purely to
// obtain its JSON and then prints a document of its own — teeing there would
// dump the raw JSON above the brief.
func captureStdoutSilent() (finish func() string, err error) {
	return captureStdoutTee(false)
}

// captureStdoutTee is the shared implementation. When tee is false the
// original stdout is dropped from the copy, so captured output never reaches
// the terminal.
func captureStdoutTee(tee bool) (finish func() string, err error) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	os.Stdout = w

	var buf bytes.Buffer
	var sink io.Writer = &buf
	if tee {
		sink = io.MultiWriter(orig, &buf)
	}

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(sink, r)
		close(done)
	}()

	return func() string {
		_ = w.Close()
		<-done
		os.Stdout = orig
		return buf.String()
	}, nil
}

// copyToClipboard pipes s into the platform's native clipboard tool.
// Windows: clip.exe. macOS: pbcopy. Other OSes return an error.
func copyToClipboard(s string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("clip.exe")
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		return fmt.Errorf("clipboard not supported on %s", runtime.GOOS)
	}
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
