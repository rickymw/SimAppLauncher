package analysis

import "testing"

func TestParseCarSetupTree_Basic(t *testing.T) {
	yaml := `WeekendInfo:
 TrackName: foo
CarSetup:
 UpdateCount: 2
 Tires:
  LeftFront:
   StartingPressure: 176 kPa
   LastHotPressure: 195 kPa
  RightFront:
   StartingPressure: 176 kPa
 Chassis:
  LeftFront:
   CornerWeight: 3114 N
SessionInfo:
 Sessions:
`
	nodes := ParseCarSetupTree(yaml)
	if len(nodes) == 0 {
		t.Fatal("expected nodes, got nil")
	}

	// Top level: UpdateCount, Tires, Chassis
	uc := FindChild(nodes, "UpdateCount")
	if uc == nil || uc.Value != "2" {
		t.Errorf("UpdateCount: got %v", uc)
	}

	tires := FindChild(nodes, "Tires")
	if tires == nil {
		t.Fatal("Tires section not found")
	}
	lf := FindChild(tires.Children, "LeftFront")
	if lf == nil {
		t.Fatal("Tires.LeftFront not found")
	}
	sp := FindChild(lf.Children, "StartingPressure")
	if sp == nil || sp.Value != "176 kPa" {
		t.Errorf("StartingPressure: got %v", sp)
	}

	chassis := FindChild(nodes, "Chassis")
	if chassis == nil {
		t.Fatal("Chassis section not found")
	}
	clf := FindChild(chassis.Children, "LeftFront")
	if clf == nil {
		t.Fatal("Chassis.LeftFront not found")
	}
	cw := FindChild(clf.Children, "CornerWeight")
	if cw == nil || cw.Value != "3114 N" {
		t.Errorf("CornerWeight: got %v", cw)
	}
}

func TestParseCarSetupTree_NotFound(t *testing.T) {
	yaml := "WeekendInfo:\n TrackName: foo\n"
	nodes := ParseCarSetupTree(yaml)
	if nodes != nil {
		t.Errorf("expected nil, got %d nodes", len(nodes))
	}
}

func TestParseCarSetupTree_StopsAtNextSection(t *testing.T) {
	yaml := `CarSetup:
 Tires:
  TireType:
   TireType: Dry
SessionInfo:
 NumSessions: 1
`
	nodes := ParseCarSetupTree(yaml)
	if nodes == nil {
		t.Fatal("expected nodes")
	}
	// Must not include SessionInfo
	if FindChild(nodes, "SessionInfo") != nil {
		t.Error("must not include SessionInfo section")
	}
}
