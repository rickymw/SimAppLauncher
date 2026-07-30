package analysis

import "testing"

func TestFlattenSetup(t *testing.T) {
	nodes := []SetupNode{
		{Key: "Chassis", Children: []SetupNode{
			{Key: "LeftFront", Children: []SetupNode{
				{Key: "Camber", Value: "-3.2 deg"},
				{Key: "ColdPressure", Value: "138 kPa"},
			}},
			{Key: "Rear", Children: []SetupNode{
				{Key: "ArbBlades", Value: "5"},
			}},
		}},
		{Key: "TiresAero", Children: []SetupNode{
			{Key: "AeroSettings", Children: []SetupNode{
				{Key: "RearWingAngle", Value: "8"},
			}},
		}},
	}

	got := FlattenSetup(nodes)
	want := []SetupValue{
		{Path: "Chassis/LeftFront/Camber", Value: "-3.2 deg"},
		{Path: "Chassis/LeftFront/ColdPressure", Value: "138 kPa"},
		{Path: "Chassis/Rear/ArbBlades", Value: "5"},
		{Path: "TiresAero/AeroSettings/RearWingAngle", Value: "8"},
	}
	if len(got) != len(want) {
		t.Fatalf("FlattenSetup returned %d leaves, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("leaf %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFlattenSetup_Empty(t *testing.T) {
	if got := FlattenSetup(nil); got != nil {
		t.Errorf("FlattenSetup(nil) = %+v, want nil", got)
	}
}

func TestDiffSetups_ChangedValue(t *testing.T) {
	older := []SetupValue{
		{Path: "Chassis/Rear/ArbBlades", Value: "5"},
		{Path: "Chassis/LeftFront/Camber", Value: "-3.2 deg"},
	}
	newer := []SetupValue{
		{Path: "Chassis/Rear/ArbBlades", Value: "3"},
		{Path: "Chassis/LeftFront/Camber", Value: "-3.2 deg"},
	}

	diff := DiffSetups(older, newer)
	if len(diff) != 1 {
		t.Fatalf("expected 1 difference, got %d: %+v", len(diff), diff)
	}
	if diff[0].Path != "Chassis/Rear/ArbBlades" || diff[0].Old != "5" || diff[0].New != "3" {
		t.Errorf("diff = %+v", diff[0])
	}
}

func TestDiffSetups_Identical(t *testing.T) {
	s := []SetupValue{{Path: "A/B", Value: "1"}}
	if diff := DiffSetups(s, s); len(diff) != 0 {
		t.Errorf("identical setups should not differ, got %+v", diff)
	}
}

// A field appearing on only one side usually means a different car, so it must
// be reported rather than quietly skipped.
func TestDiffSetups_AddedAndRemovedFields(t *testing.T) {
	older := []SetupValue{
		{Path: "Chassis/OnlyOld", Value: "7"},
		{Path: "Chassis/Shared", Value: "1"},
	}
	newer := []SetupValue{
		{Path: "Chassis/Shared", Value: "1"},
		{Path: "Chassis/OnlyNew", Value: "9"},
	}

	diff := DiffSetups(older, newer)
	byPath := map[string]SetupDiffEntry{}
	for _, d := range diff {
		byPath[d.Path] = d
	}

	added, ok := byPath["Chassis/OnlyNew"]
	if !ok {
		t.Fatal("field added in the new setup was not reported")
	}
	if added.Old != "" || added.New != "9" {
		t.Errorf("added field = %+v, want Old empty and New 9", added)
	}

	removed, ok := byPath["Chassis/OnlyOld"]
	if !ok {
		t.Fatal("field missing from the new setup was not reported")
	}
	if removed.Old != "7" || removed.New != "" {
		t.Errorf("removed field = %+v, want Old 7 and New empty", removed)
	}

	if _, ok := byPath["Chassis/Shared"]; ok {
		t.Error("unchanged shared field should not appear in the diff")
	}
}

func TestIsSessionState(t *testing.T) {
	stateful := []string{
		"UpdateCount",
		"Tires/LeftFront/LastHotPressure",
		"Tires/LeftFront/LastTempsOMI",
		"Tires/RightFront/LastTempsIMO",
		"Tires/RightRear/TreadRemaining",
	}
	for _, p := range stateful {
		if !IsSessionState(p) {
			t.Errorf("IsSessionState(%q) = false, want true", p)
		}
	}

	adjustable := []string{
		"Chassis/Rear/ArbBlades",
		"Chassis/LeftFront/SpringPerchOffset",
		"Tires/LeftFront/StartingPressure",
		"Chassis/Rear/WingAngle",
	}
	for _, p := range adjustable {
		if IsSessionState(p) {
			t.Errorf("IsSessionState(%q) = true, want false", p)
		}
	}
}

// The whole point of the filter: two identical setups still differ on every
// tyre reading, which would otherwise bury the real changes.
func TestFilterSessionState(t *testing.T) {
	diff := []SetupDiffEntry{
		{Path: "UpdateCount", Old: "5", New: "1"},
		{Path: "Tires/LeftFront/LastHotPressure", Old: "192 kPa", New: "176 kPa"},
		{Path: "Tires/LeftFront/TreadRemaining", Old: "94%", New: "100%"},
		{Path: "Chassis/Rear/ArbBlades", Old: "5", New: "6"},
		{Path: "Chassis/Rear/WingAngle", Old: "4 deg", New: "2 deg"},
	}

	kept, hidden := FilterSessionState(diff)
	if hidden != 3 {
		t.Errorf("hidden = %d, want 3", hidden)
	}
	if len(kept) != 2 {
		t.Fatalf("kept %d entries, want 2: %+v", len(kept), kept)
	}
	for _, k := range kept {
		if IsSessionState(k.Path) {
			t.Errorf("session-state path %q survived the filter", k.Path)
		}
	}
	// Order among the kept entries must be preserved.
	if kept[0].Path != "Chassis/Rear/ArbBlades" || kept[1].Path != "Chassis/Rear/WingAngle" {
		t.Errorf("filter reordered entries: %+v", kept)
	}
}

func TestFilterSessionState_Empty(t *testing.T) {
	kept, hidden := FilterSessionState(nil)
	if kept != nil || hidden != 0 {
		t.Errorf("FilterSessionState(nil) = %+v, %d; want nil, 0", kept, hidden)
	}
}

func TestDiffSetups_PreservesNewerOrder(t *testing.T) {
	older := []SetupValue{
		{Path: "A", Value: "1"},
		{Path: "B", Value: "1"},
		{Path: "C", Value: "1"},
	}
	newer := []SetupValue{
		{Path: "C", Value: "2"},
		{Path: "A", Value: "2"},
		{Path: "B", Value: "2"},
	}
	diff := DiffSetups(older, newer)
	if len(diff) != 3 {
		t.Fatalf("expected 3 differences, got %d", len(diff))
	}
	for i, want := range []string{"C", "A", "B"} {
		if diff[i].Path != want {
			t.Errorf("diff[%d].Path = %q, want %q", i, diff[i].Path, want)
		}
	}
}
