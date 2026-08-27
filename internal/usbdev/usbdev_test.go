package usbdev

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseVIDPID(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		wantVID    uint16
		wantPID    uint16
		wantOK     bool
	}{
		{
			name:       "top level usb device",
			instanceID: `USB\VID_30B7&PID_1001\SP24776C025F602628`,
			wantVID:    0x30B7, wantPID: 0x1001, wantOK: true,
		},
		{
			name:       "lowercase hex and enumerator",
			instanceID: `usb\vid_346e&pid_001f\1D003A001557434232393420`,
			wantVID:    0x346E, wantPID: 0x001F, wantOK: true,
		},
		{
			// A REV_ field sits between VID_ and PID_ on some devices and must
			// not throw off the parse.
			name:       "extra rev field",
			instanceID: `USB\VID_0FD9&PID_0098&REV_0360\A1279FB64`,
			wantVID:    0x0FD9, wantPID: 0x0098, wantOK: true,
		},
		{
			// The composite handbrake publishes one node per interface. Those
			// are children of the device, not the device.
			name:       "interface node is rejected",
			instanceID: `USB\VID_346E&PID_001F&MI_02\a&2f4cb59a&0&0002`,
			wantOK:     false,
		},
		{
			name:       "HID node is not a USB device node",
			instanceID: `HID\VID_3670&PID_0902\9&1FBACC12&0&0000`,
			wantOK:     false,
		},
		{
			name:       "no product id",
			instanceID: `USB\VID_30B7\SP24776C025F602628`,
			wantOK:     false,
		},
		{
			name:       "no instance segment",
			instanceID: `USB`,
			wantOK:     false,
		},
		{
			name:       "empty",
			instanceID: ``,
			wantOK:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vid, pid, ok := parseVIDPID(tc.instanceID)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if vid != tc.wantVID || pid != tc.wantPID {
				t.Errorf("got VID_%04X PID_%04X, want VID_%04X PID_%04X", vid, pid, tc.wantVID, tc.wantPID)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	known, ok := match(`USB\VID_30B7&PID_1001\SP24776C025F602628`)
	if !ok {
		t.Fatal("expected the Heusinkveld pedals to match")
	}
	if known.Alias != "pedals" {
		t.Errorf("alias = %q, want %q", known.Alias, "pedals")
	}

	if _, ok := match(`USB\VID_1234&PID_5678\whatever`); ok {
		t.Error("expected an unknown vendor not to match")
	}
}

func testDevices() []Device {
	return []Device{
		{Known: Known{Alias: "handbrake", Name: "MOZA HBP Handbrake"}, InstanceID: `USB\A`, State: StateEnabled},
		{Known: Known{Alias: "haptic", Name: "SIMAGIC P2000 Haptic"}, InstanceID: `USB\B`, State: StateDisabled},
		{Known: Known{Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint"}, InstanceID: `USB\C`, State: StateEnabled},
		{Known: Known{Alias: "wheelbase", Name: "SIMAGIC Alpha Series Wheelbase"}, State: StateAbsent},
	}
}

func TestResolveExactAlias(t *testing.T) {
	got, err := Resolve(testDevices(), "PEDALS")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Alias != "pedals" {
		t.Fatalf("got %v, want exactly the pedals", got)
	}
}

// TestResolveExactAliasBeatsSubstring guards the precedence rule: an exact
// alias must win outright, or adding a device whose name contains another
// device's alias would make a previously working command ambiguous.
func TestResolveExactAliasBeatsSubstring(t *testing.T) {
	devs := append(testDevices(), Device{
		Known:      Known{Alias: "spare", Name: "Spare pedals rig"},
		InstanceID: `USB\D`, State: StateEnabled,
	})
	got, err := Resolve(devs, "pedals")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Alias != "pedals" {
		t.Fatalf("got %v, want the exact alias match", got)
	}
}

func TestResolveSubstring(t *testing.T) {
	got, err := Resolve(testDevices(), "heusink")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Alias != "pedals" {
		t.Fatalf("got %v, want the pedals", got)
	}
}

func TestResolveAmbiguousRefusesToGuess(t *testing.T) {
	_, err := Resolve(testDevices(), "simagic")
	if err == nil {
		t.Fatal("expected an error for a target matching two devices")
	}
	if !strings.Contains(err.Error(), "haptic") || !strings.Contains(err.Error(), "wheelbase") {
		t.Errorf("expected both candidates named, got: %v", err)
	}
}

func TestResolveUnknownListsAliases(t *testing.T) {
	_, err := Resolve(testDevices(), "pedls")
	if err == nil {
		t.Fatal("expected an error for an unknown target")
	}
	if !strings.Contains(err.Error(), "pedals") {
		t.Errorf("expected the known aliases listed, got: %v", err)
	}
}

// TestResolveAllIncludesAbsent: the caller reports and skips unplugged devices,
// so filtering them here would hide the wheelbase from `usb off all` entirely.
func TestResolveAllIncludesAbsent(t *testing.T) {
	got, err := Resolve(testDevices(), "all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d devices, want all 4 known ones", len(got))
	}
}

func TestResolveAllWithNoKnownDevices(t *testing.T) {
	if _, err := Resolve(nil, "all"); err == nil {
		t.Fatal("expected an error when there are no devices at all")
	}
}

func TestResolveEmptyTarget(t *testing.T) {
	if _, err := Resolve(testDevices(), "   "); err == nil {
		t.Fatal("expected an error for an empty target")
	}
}

func TestStateString(t *testing.T) {
	cases := map[State]string{
		StateEnabled:  "enabled",
		StateDisabled: "disabled",
		StateAbsent:   "not connected",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("State(%d) = %q, want %q", state, got, want)
		}
	}
}

func TestSortDevices(t *testing.T) {
	devs := []Device{
		{Known: Known{Alias: "pedals"}},
		{Known: Known{Alias: "handbrake"}},
		{Known: Known{Alias: "haptic"}},
	}
	SortDevices(devs)
	want := []string{"handbrake", "haptic", "pedals"}
	for i, w := range want {
		if devs[i].Alias != w {
			t.Fatalf("position %d = %q, want %q", i, devs[i].Alias, w)
		}
	}
}

func TestFormatList(t *testing.T) {
	var buf bytes.Buffer
	FormatList(&buf, testDevices(), false)
	out := buf.String()

	for _, want := range []string{"handbrake", "MOZA HBP Handbrake", "disabled", "not connected"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, `USB\A`) {
		t.Errorf("instance IDs should be hidden without -v, got:\n%s", out)
	}
}

func TestFormatListVerbose(t *testing.T) {
	var buf bytes.Buffer
	FormatList(&buf, testDevices(), true)
	out := buf.String()

	if !strings.Contains(out, `USB\A`) {
		t.Errorf("expected instance IDs with -v, got:\n%s", out)
	}

	// The absent wheelbase has no instance ID, so verbose mode must not emit a
	// blank continuation line under it.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, l := range lines {
		if !strings.Contains(l, "wheelbase") {
			continue
		}
		if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
			t.Errorf("blank continuation line under the absent device, got:\n%s", out)
		}
	}
}
