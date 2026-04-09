//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- vkToName ----

func TestVkToName_FKeys(t *testing.T) {
	cases := []struct {
		vk   uint32
		want string
	}{
		{0x70, "F1"},
		{0x71, "F2"},
		{0x7C, "F13"},
		{0x87, "F24"},
	}
	for _, tc := range cases {
		got := vkToName(tc.vk)
		if got != tc.want {
			t.Errorf("vkToName(0x%02X) = %q, want %q", tc.vk, got, tc.want)
		}
	}
}

func TestVkToName_NamedKeys(t *testing.T) {
	cases := []struct {
		vk   uint32
		want string
	}{
		{0x91, "ScrollLock"},
		{0x13, "Pause"},
		{0x2D, "Insert"},
	}
	for _, tc := range cases {
		got := vkToName(tc.vk)
		if got != tc.want {
			t.Errorf("vkToName(0x%02X) = %q, want %q", tc.vk, got, tc.want)
		}
	}
}

func TestVkToName_UnknownFallsBackToHex(t *testing.T) {
	// 0x41 = 'A' key — not in namedVKs, not an F key.
	got := vkToName(0x41)
	if got != "0x41" {
		t.Errorf("vkToName(0x41) = %q, want 0x41", got)
	}
}

// ---- parseVKey ----

func TestParseVKey_FKeys(t *testing.T) {
	cases := []struct {
		input string
		want  uint32
	}{
		{"F1", 0x70},
		{"F13", 0x7C},
		{"F24", 0x87},
	}
	for _, tc := range cases {
		got, err := parseVKey(tc.input)
		if err != nil {
			t.Errorf("parseVKey(%q): unexpected error %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseVKey(%q) = 0x%02X, want 0x%02X", tc.input, got, tc.want)
		}
	}
}

func TestParseVKey_NamedKeys(t *testing.T) {
	cases := []struct {
		input string
		want  uint32
	}{
		{"ScrollLock", 0x91},
		{"scrolllock", 0x91}, // case-insensitive
		{"Pause", 0x13},
		{"Insert", 0x2D},
	}
	for _, tc := range cases {
		got, err := parseVKey(tc.input)
		if err != nil {
			t.Errorf("parseVKey(%q): unexpected error %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseVKey(%q) = 0x%02X, want 0x%02X", tc.input, got, tc.want)
		}
	}
}

func TestParseVKey_HexInput(t *testing.T) {
	got, err := parseVKey("0x91")
	if err != nil {
		t.Fatalf("parseVKey(\"0x91\"): %v", err)
	}
	if got != 0x91 {
		t.Errorf("parseVKey(\"0x91\") = 0x%02X, want 0x91", got)
	}
}

func TestParseVKey_DecimalInput(t *testing.T) {
	got, err := parseVKey("145") // 145 = 0x91
	if err != nil {
		t.Fatalf("parseVKey(\"145\"): %v", err)
	}
	if got != 145 {
		t.Errorf("parseVKey(\"145\") = %d, want 145", got)
	}
}

func TestParseVKey_InvalidInput(t *testing.T) {
	_, err := parseVKey("notakey")
	if err == nil {
		t.Error("parseVKey(\"notakey\"): expected error, got nil")
	}
}

func TestParseVKey_RoundtripWithVkToName(t *testing.T) {
	// vkToName → parseVKey should be a stable roundtrip for F keys and named keys.
	vks := []uint32{0x70, 0x7C, 0x87, 0x91, 0x13} // F1, F13, F24, ScrollLock, Pause
	for _, vk := range vks {
		name := vkToName(vk)
		got, err := parseVKey(name)
		if err != nil {
			t.Errorf("parseVKey(vkToName(0x%02X)=%q): %v", vk, name, err)
			continue
		}
		if got != vk {
			t.Errorf("roundtrip 0x%02X → %q → 0x%02X (mismatch)", vk, name, got)
		}
	}
}

// ---- findRecentIbt ----

func TestFindRecentIbt_ReturnsRecentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.ibt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	os.Chtimes(path, now, now)

	got := findRecentIbt(dir, time.Hour)
	if filepath.Base(got) != "session.ibt" {
		t.Errorf("findRecentIbt = %q, want session.ibt", got)
	}
}

func TestFindRecentIbt_IgnoresOldFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.ibt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Hour)
	os.Chtimes(path, old, old)

	got := findRecentIbt(dir, time.Hour)
	if got != "" {
		t.Errorf("findRecentIbt = %q, want empty string (file is too old)", got)
	}
}

func TestFindRecentIbt_IgnoresNonIbtFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got := findRecentIbt(dir, time.Hour)
	if got != "" {
		t.Errorf("findRecentIbt = %q, want empty string (no .ibt files)", got)
	}
}

func TestFindRecentIbt_ReturnsNewestAmongMultiple(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	files := []struct {
		name string
		age  time.Duration
	}{
		{"a.ibt", 30 * time.Minute},
		{"b.ibt", 10 * time.Minute}, // newest
		{"c.ibt", 20 * time.Minute},
	}
	for _, f := range files {
		p := filepath.Join(dir, f.name)
		os.WriteFile(p, []byte("x"), 0644)
		mt := now.Add(-f.age)
		os.Chtimes(p, mt, mt)
	}

	got := findRecentIbt(dir, time.Hour)
	if filepath.Base(got) != "b.ibt" {
		t.Errorf("findRecentIbt = %q, want b.ibt (newest)", filepath.Base(got))
	}
}

func TestFindRecentIbt_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got := findRecentIbt(dir, time.Hour)
	if got != "" {
		t.Errorf("findRecentIbt empty dir = %q, want empty string", got)
	}
}
