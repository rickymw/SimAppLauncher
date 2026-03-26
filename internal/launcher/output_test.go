package launcher

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(f func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestPrintLaunched(t *testing.T) {
	out := captureStdout(func() { PrintLaunched("SimHub", 1234) })
	if !strings.Contains(out, "[+]") {
		t.Errorf("missing [+] in: %q", out)
	}
	if !strings.Contains(out, "SimHub") {
		t.Errorf("missing app name in: %q", out)
	}
	if !strings.Contains(out, "1234") {
		t.Errorf("missing pid in: %q", out)
	}
}

func TestPrintFailed(t *testing.T) {
	out := captureStdout(func() { PrintFailed("SimHub", "path not found") })
	if !strings.Contains(out, "[!]") {
		t.Errorf("missing [!] in: %q", out)
	}
	if !strings.Contains(out, "path not found") {
		t.Errorf("missing reason in: %q", out)
	}
}

func TestPrintClosed(t *testing.T) {
	out := captureStdout(func() { PrintClosed("SimHub") })
	if !strings.Contains(out, "[-]") {
		t.Errorf("missing [-] in: %q", out)
	}
	if !strings.Contains(out, "SimHub") {
		t.Errorf("missing app name in: %q", out)
	}
}

func TestPrintStatus_Running(t *testing.T) {
	out := captureStdout(func() { PrintStatus("SimHub", true, 5678) })
	if !strings.Contains(out, "RUNNING") {
		t.Errorf("missing RUNNING in: %q", out)
	}
	if !strings.Contains(out, "5678") {
		t.Errorf("missing pid in: %q", out)
	}
}

func TestPrintStatus_Stopped(t *testing.T) {
	out := captureStdout(func() { PrintStatus("SimHub", false, 0) })
	if !strings.Contains(out, "STOPPED") {
		t.Errorf("missing STOPPED in: %q", out)
	}
	if !strings.Contains(out, "-") {
		t.Errorf("missing - for pid in: %q", out)
	}
}
