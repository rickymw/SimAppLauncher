package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/config"
)

// The notes file is named after the .ibt so that analyze can find it later by
// filename — that naming is the entire join between notes and telemetry.
func TestResolveSessionPath_NamesFileAfterIbt(t *testing.T) {
	notesDir := filepath.Join(t.TempDir(), "notes")
	ibtDir := t.TempDir()

	ibt := filepath.Join(ibtDir, "porsche718 watkinsglen 2026-07-30.ibt")
	if err := os.WriteFile(ibt, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionPath, ibtFile := resolveSessionPath(notesDir, ibtDir)

	wantPath := filepath.Join(notesDir, "porsche718 watkinsglen 2026-07-30.json")
	if sessionPath != wantPath {
		t.Errorf("sessionPath = %q, want %q", sessionPath, wantPath)
	}
	if ibtFile != "porsche718 watkinsglen 2026-07-30.ibt" {
		t.Errorf("ibtFile = %q", ibtFile)
	}

	// The name it produces must be the one analyze looks for.
	if got := notesFileForIbt(notesDir, ibt); got != sessionPath {
		t.Errorf("notesFileForIbt = %q but resolveSessionPath = %q — the join is broken", got, sessionPath)
	}
}

func TestResolveSessionPath_CreatesNotesDir(t *testing.T) {
	notesDir := filepath.Join(t.TempDir(), "notes")

	resolveSessionPath(notesDir, "")

	if fi, err := os.Stat(notesDir); err != nil || !fi.IsDir() {
		t.Errorf("notes dir was not created: %v", err)
	}
}

// With no recent .ibt there is nothing to name the file after, so it falls
// back to a timestamp and reports no associated .ibt.
func TestResolveSessionPath_FallsBackToTimestamp(t *testing.T) {
	notesDir := filepath.Join(t.TempDir(), "notes")

	sessionPath, ibtFile := resolveSessionPath(notesDir, "")

	if ibtFile != "" {
		t.Errorf("ibtFile = %q, want empty when no .ibt was found", ibtFile)
	}
	base := filepath.Base(sessionPath)
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}-\d{2}-\d{2}\.json$`).MatchString(base) {
		t.Errorf("expected a timestamp filename, got %q", base)
	}
}

// A stale .ibt from a previous day must not capture this session's notes.
func TestResolveSessionPath_IgnoresOldIbt(t *testing.T) {
	notesDir := filepath.Join(t.TempDir(), "notes")
	ibtDir := t.TempDir()

	old := filepath.Join(ibtDir, "yesterday.ibt")
	if err := os.WriteFile(old, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatal(err)
	}

	_, ibtFile := resolveSessionPath(notesDir, ibtDir)
	if ibtFile != "" {
		t.Errorf("ibtFile = %q, want empty — a day-old .ibt is not this session", ibtFile)
	}
}

// ---- whisper path resolution ----

func TestResolveWhisperPaths_MissingConfig(t *testing.T) {
	if _, _, err := resolveWhisperPaths(config.Config{}); err == nil {
		t.Error("expected an error when whisperPath is unset")
	} else if !strings.Contains(err.Error(), "whisperPath") {
		t.Errorf("error should name the missing field, got %v", err)
	}

	cfg := config.Config{WhisperPath: "whisper-cli.exe"}
	if _, _, err := resolveWhisperPaths(cfg); err == nil {
		t.Error("expected an error when whisperModel is unset")
	} else if !strings.Contains(err.Error(), "whisperModel") {
		t.Errorf("error should name the missing field, got %v", err)
	}
}

func TestResolveWhisperPaths_AbsolutePaths(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "whisper-cli.exe")
	model := filepath.Join(dir, "ggml-base.en.bin")
	for _, p := range []string{exe, model} {
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	gotExe, gotModel, err := resolveWhisperPaths(config.Config{
		WhisperPath:  exe,
		WhisperModel: model,
	})
	if err != nil {
		t.Fatalf("resolveWhisperPaths: %v", err)
	}
	if gotExe != exe || gotModel != model {
		t.Errorf("absolute paths should pass through unchanged: %q %q", gotExe, gotModel)
	}
}

// A configured path that does not exist has to fail loudly, naming the path —
// otherwise transcription fails later with an opaque exec error.
func TestResolveWhisperPaths_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-there.exe")

	_, _, err := resolveWhisperPaths(config.Config{
		WhisperPath:  missing,
		WhisperModel: filepath.Join(dir, "model.bin"),
	})
	if err == nil {
		t.Fatal("expected an error for a missing whisper-cli")
	}
	// The message quotes the path with %q, which escapes Windows separators —
	// compare against the same quoted form rather than the raw path.
	if !strings.Contains(err.Error(), strconv.Quote(missing)) {
		t.Errorf("error should name the path it looked for, got %v", err)
	}
}

func TestResolveWhisperPaths_MissingModelOnly(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "whisper-cli.exe")
	if err := os.WriteFile(exe, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := resolveWhisperPaths(config.Config{
		WhisperPath:  exe,
		WhisperModel: filepath.Join(dir, "absent.bin"),
	})
	if err == nil {
		t.Fatal("expected an error for a missing model")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("error should mention the model, got %v", err)
	}
}

// ---- clipboard ----

func TestCopyToClipboard(t *testing.T) {
	// clip.exe is present on every supported Windows install; this is the same
	// call the analyze flow makes after every run.
	if err := copyToClipboard("motorhome clipboard test"); err != nil {
		t.Errorf("copyToClipboard: %v", err)
	}
}
