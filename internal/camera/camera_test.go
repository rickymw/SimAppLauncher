package camera

import (
	"bytes"
	"errors"
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

type mockRestarter struct {
	restartFn func() ([]ServiceResult, error)
}

func (m *mockRestarter) Restart(progress func(string)) ([]ServiceResult, error) {
	return m.restartFn()
}

func TestRunCameraRestart_Success(t *testing.T) {
	called := false
	r := &mockRestarter{restartFn: func() ([]ServiceResult, error) {
		called = true
		return []ServiceResult{
			{Name: "FrameServer", Restarted: true},
			{Name: "FrameServerMonitor", Restarted: true},
		}, nil
	}}

	out := captureStdout(func() { RunCameraRestart(r) })

	if !called {
		t.Errorf("expected Restart to be called")
	}
	if !strings.Contains(out, "[+]") {
		t.Errorf("expected success marker [+] in output, got: %q", out)
	}
	if !strings.Contains(out, "FrameServer") {
		t.Errorf("expected service name in output, got: %q", out)
	}
	if !strings.Contains(out, "2/2") {
		t.Errorf("expected 2/2 in summary, got: %q", out)
	}
}

// TestRunCameraRestart_AlreadyStopped verifies that services left alone are
// reported as such, and not counted as restarts.
func TestRunCameraRestart_AlreadyStopped(t *testing.T) {
	r := &mockRestarter{restartFn: func() ([]ServiceResult, error) {
		return []ServiceResult{
			{Name: "FrameServer", Restarted: false},
			{Name: "FrameServerMonitor", Restarted: false},
		}, nil
	}}

	out := captureStdout(func() { RunCameraRestart(r) })

	if !strings.Contains(out, "[=]") {
		t.Errorf("expected already-stopped marker [=] in output, got: %q", out)
	}
	if !strings.Contains(out, "nothing to restart") {
		t.Errorf("expected nothing-to-restart note in output, got: %q", out)
	}
	if strings.Contains(out, "[+]") {
		t.Errorf("unexpected restart marker [+] in output: %q", out)
	}
}

// TestRunCameraRestart_Mixed verifies a partial restart is summarised correctly.
func TestRunCameraRestart_Mixed(t *testing.T) {
	r := &mockRestarter{restartFn: func() ([]ServiceResult, error) {
		return []ServiceResult{
			{Name: "FrameServer", Restarted: true},
			{Name: "FrameServerMonitor", Restarted: false},
		}, nil
	}}

	out := captureStdout(func() { RunCameraRestart(r) })

	if !strings.Contains(out, "1/2") {
		t.Errorf("expected 1/2 in summary, got: %q", out)
	}
}

// TestRunCameraRestart_ProgressPrinted verifies the progress callback is passed
// through and its messages reach stdout — without it a slow stop (the camera is
// in use and Windows waits ~30s for the holder to release it) looks like a hang.
func TestRunCameraRestart_ProgressPrinted(t *testing.T) {
	r := &mockRestarterProgress{restartFn: func(progress func(string)) ([]ServiceResult, error) {
		if progress == nil {
			t.Fatal("expected a non-nil progress callback")
		}
		progress("      FrameServer is still in use — waiting")
		return []ServiceResult{{Name: "FrameServer", Restarted: true}}, nil
	}}

	out := captureStdout(func() { RunCameraRestart(r) })

	if !strings.Contains(out, "still in use") {
		t.Errorf("expected progress message in output, got: %q", out)
	}
}

type mockRestarterProgress struct {
	restartFn func(progress func(string)) ([]ServiceResult, error)
}

func (m *mockRestarterProgress) Restart(progress func(string)) ([]ServiceResult, error) {
	return m.restartFn(progress)
}

func TestRunCameraRestart_Failure(t *testing.T) {
	r := &mockRestarter{restartFn: func() ([]ServiceResult, error) {
		return nil, errors.New("access is denied")
	}}

	out := captureStdout(func() { RunCameraRestart(r) })

	if !strings.Contains(out, "[!]") {
		t.Errorf("expected failure marker [!] in output, got: %q", out)
	}
	if !strings.Contains(out, "access is denied") {
		t.Errorf("expected reason in output, got: %q", out)
	}
}
