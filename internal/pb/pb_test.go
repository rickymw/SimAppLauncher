package pb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKey(t *testing.T) {
	got := Key("Porsche 911 GT3 R", "Sebring")
	want := "Porsche 911 GT3 R|Sebring"
	if got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}

func TestKey_EmptyFields(t *testing.T) {
	got := Key("", "")
	if got != "|" {
		t.Errorf("Key(\"\",\"\") = %q, want \"|\"", got)
	}
}

// ---- Load ----

func TestLoad_FileNotFound(t *testing.T) {
	f, err := Load("nonexistent_pb_xyzzy.json")
	if err != nil {
		t.Fatalf("Load missing file: got error %v, want nil", err)
	}
	if len(f) != 0 {
		t.Errorf("Load missing file: got non-empty map")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")
	os.WriteFile(p, []byte("not json {{"), 0644)

	_, err := Load(p)
	if err == nil {
		t.Error("Load invalid JSON: expected error, got nil")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")

	pbf := File{
		"GT3|Sebring": {
			LapTime:          131.5,
			LapTimeFormatted: "2:11.500",
			Date:             "2026-03-01",
			Weather:          "Air 22°C, Track 35°C",
			Car:              "GT3",
			Track:            "Sebring",
		},
	}
	if err := Save(p, pbf); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := loaded["GT3|Sebring"]
	if entry == nil {
		t.Fatal("entry not found after load")
	}
	if entry.LapTime != 131.5 {
		t.Errorf("LapTime = %v, want 131.5", entry.LapTime)
	}
	if entry.LapTimeFormatted != "2:11.500" {
		t.Errorf("LapTimeFormatted = %q, want 2:11.500", entry.LapTimeFormatted)
	}
	if entry.Weather != "Air 22°C, Track 35°C" {
		t.Errorf("Weather = %q, want Air 22°C, Track 35°C", entry.Weather)
	}
}

// ---- Save ----

func TestSave_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")

	if err := Save(p, File{}); err != nil {
		t.Fatalf("Save empty file: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestSave_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "pb.json")

	orig := File{
		"Car A|Track X": {LapTime: 90.0, LapTimeFormatted: "1:30.000", Date: "2026-01-01", Car: "Car A", Track: "Track X"},
		"Car B|Track Y": {LapTime: 75.5, LapTimeFormatted: "1:15.500", Date: "2026-02-01", Car: "Car B", Track: "Track Y"},
	}
	if err := Save(p, orig); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	if got["Car B|Track Y"].LapTime != 75.5 {
		t.Errorf("LapTime = %v, want 75.5", got["Car B|Track Y"].LapTime)
	}
}

// ---- Update ----

func TestUpdate_NewEntry(t *testing.T) {
	pbf := File{}
	isNew := Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "Air 22°C")
	if !isNew {
		t.Error("Update new entry: expected true, got false")
	}
	entry := pbf[Key("GT3", "Sebring")]
	if entry == nil {
		t.Fatal("entry not stored after Update")
	}
	if entry.LapTime != 131.5 {
		t.Errorf("LapTime = %v, want 131.5", entry.LapTime)
	}
	if entry.Car != "GT3" {
		t.Errorf("Car = %q, want GT3", entry.Car)
	}
	if entry.Track != "Sebring" {
		t.Errorf("Track = %q, want Sebring", entry.Track)
	}
}

func TestUpdate_FasterLap_ReplacesPB(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")

	isNew := Update(pbf, "GT3", "Sebring", 130.0, "2:10.000", "2026-03-02", "")
	if !isNew {
		t.Error("Update faster lap: expected true, got false")
	}
	if pbf[Key("GT3", "Sebring")].LapTime != 130.0 {
		t.Errorf("LapTime = %v, want 130.0 after PB improvement", pbf[Key("GT3", "Sebring")].LapTime)
	}
}

func TestUpdate_SlowerLap_KeepsPB(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "")

	isNew := Update(pbf, "GT3", "Sebring", 135.0, "2:15.000", "2026-03-02", "")
	if isNew {
		t.Error("Update slower lap: expected false, got true")
	}
	if pbf[Key("GT3", "Sebring")].LapTime != 131.5 {
		t.Errorf("LapTime = %v, want 131.5 (PB should not change)", pbf[Key("GT3", "Sebring")].LapTime)
	}
}

func TestUpdate_EqualLap_KeepsPB(t *testing.T) {
	pbf := File{}
	Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-01", "old weather")

	isNew := Update(pbf, "GT3", "Sebring", 131.5, "2:11.500", "2026-03-02", "new weather")
	if isNew {
		t.Error("Update equal lap: expected false, got true")
	}
	// Original entry should be unchanged.
	if pbf[Key("GT3", "Sebring")].Weather != "old weather" {
		t.Error("weather changed on equal laptime — original PB entry should be kept")
	}
}

func TestUpdate_IndependentCarTrackCombos(t *testing.T) {
	pbf := File{}
	Update(pbf, "Car A", "Track X", 100.0, "1:40.000", "2026-01-01", "")
	Update(pbf, "Car A", "Track Y", 90.0, "1:30.000", "2026-01-01", "")
	Update(pbf, "Car B", "Track X", 80.0, "1:20.000", "2026-01-01", "")

	if len(pbf) != 3 {
		t.Errorf("len = %d, want 3", len(pbf))
	}
	if pbf[Key("Car A", "Track X")].LapTime != 100.0 {
		t.Errorf("Car A / Track X laptime wrong")
	}
	if pbf[Key("Car B", "Track X")].LapTime != 80.0 {
		t.Errorf("Car B / Track X laptime wrong")
	}
}
