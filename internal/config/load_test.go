package config

import (
	"os"
	"testing"
)

func TestLoad_ValidConfig(t *testing.T) {
	json := `{
		"logFile": "test.log",
		"apps": [
			{
				"name": "TestApp",
				"path": "C:\\test\\app.exe",
				"args": "",
				"windowStyle": "Normal",
				"delayMs": 500,
				"elevate": false,
				"processName": "app"
			}
		]
	}`
	f := writeTempFile(t, json)

	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogFile != "test.log" {
		t.Errorf("LogFile = %q, want %q", cfg.LogFile, "test.log")
	}
	if len(cfg.Apps) != 1 {
		t.Fatalf("len(Apps) = %d, want 1", len(cfg.Apps))
	}
	app := cfg.Apps[0]
	if app.Name != "TestApp" {
		t.Errorf("Name = %q, want %q", app.Name, "TestApp")
	}
	if app.DelayMs != 500 {
		t.Errorf("DelayMs = %d, want 500", app.DelayMs)
	}
	if app.Elevate {
		t.Error("Elevate = true, want false")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("nonexistent_file_that_does_not_exist.json")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	f := writeTempFile(t, `{ "logFile": "bad json" `)

	_, err := Load(f)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestLoad_EmptyApps(t *testing.T) {
	f := writeTempFile(t, `{"logFile": "", "apps": []}`)

	cfg, err := Load(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Apps) != 0 {
		t.Errorf("len(Apps) = %d, want 0", len(cfg.Apps))
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "simapp-test-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	f.Close()
	return f.Name()
}
