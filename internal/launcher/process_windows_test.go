//go:build windows

package launcher

import (
	"testing"
)

// TestParsePIDFromTasklist_Found verifies a normal match returns the correct PID.
func TestParsePIDFromTasklist_Found(t *testing.T) {
	output := `"simhub.exe","1234","Console","1","50,348 K"` + "\r\n"
	pid, err := parsePIDFromTasklist(output, "simhub")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if pid != 1234 {
		t.Errorf("pid = %d, want 1234", pid)
	}
}

// TestParsePIDFromTasklist_NotFound verifies an error is returned when the process is absent.
func TestParsePIDFromTasklist_NotFound(t *testing.T) {
	output := `"explorer.exe","888","Console","1","12,345 K"` + "\r\n"
	_, err := parsePIDFromTasklist(output, "simhub")
	if err == nil {
		t.Error("expected error for missing process, got nil")
	}
}

// TestParsePIDFromTasklist_CaseInsensitive verifies matching is case-insensitive.
func TestParsePIDFromTasklist_CaseInsensitive(t *testing.T) {
	// tasklist output may capitalise the exe name differently from the config value.
	output := `"SimHub.exe","5678","Console","1","100,000 K"` + "\r\n"
	pid, err := parsePIDFromTasklist(output, "simhub")
	if err != nil {
		t.Fatalf("expected match, got error: %v", err)
	}
	if pid != 5678 {
		t.Errorf("pid = %d, want 5678", pid)
	}
}

// TestParsePIDFromTasklist_MultipleProcesses verifies the first matching line is returned.
func TestParsePIDFromTasklist_MultipleProcesses(t *testing.T) {
	output := "\"otherapp.exe\",\"100\",\"Console\",\"1\",\"10,000 K\"\r\n" +
		"\"simhub.exe\",\"2222\",\"Console\",\"1\",\"50,000 K\"\r\n" +
		"\"simhub.exe\",\"3333\",\"Console\",\"1\",\"50,000 K\"\r\n"
	pid, err := parsePIDFromTasklist(output, "simhub")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != 2222 {
		t.Errorf("pid = %d, want 2222 (first match)", pid)
	}
}

// TestParsePIDFromTasklist_EmptyOutput verifies an error for blank output (no processes running).
func TestParsePIDFromTasklist_EmptyOutput(t *testing.T) {
	_, err := parsePIDFromTasklist("", "simhub")
	if err == nil {
		t.Error("expected error for empty output, got nil")
	}
}

// TestParsePIDFromTasklist_InformationalLine verifies the "INFO: No tasks..." line is ignored.
func TestParsePIDFromTasklist_InformationalLine(t *testing.T) {
	output := "INFO: No tasks are running which match the specified criteria.\r\n"
	_, err := parsePIDFromTasklist(output, "simhub")
	if err == nil {
		t.Error("expected error (process not found), got nil")
	}
}
