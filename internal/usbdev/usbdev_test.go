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
	known, ok := match(KnownDevices, `USB\VID_30B7&PID_1001\SP24776C025F602628`)
	if !ok {
		t.Fatal("expected the Heusinkveld pedals to match")
	}
	if known.Alias != "pedals" {
		t.Errorf("alias = %q, want %q", known.Alias, "pedals")
	}

	if _, ok := match(KnownDevices, `USB\VID_1234&PID_5678\whatever`); ok {
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
	FormatList(&buf, testDevices(), false, false)
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
	FormatList(&buf, testDevices(), true, false)
	out := buf.String()

	if !strings.Contains(out, `USB\A`) {
		t.Errorf("expected instance IDs with -v, got:\n%s", out)
	}

	// The absent wheelbase has no instance ID, so verbose mode must not emit a
	// blank continuation line under it.
	//
	// A continuation is the padded format string with an empty ID, so it comes
	// out as whitespace rather than as nothing. That is what distinguishes it
	// from the genuinely empty line separating the table from its footer, which
	// also falls under the last device.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, l := range lines {
		if !strings.Contains(l, "wheelbase") {
			continue
		}
		if i+1 >= len(lines) {
			continue
		}
		next := lines[i+1]
		if next != "" && strings.TrimSpace(next) == "" {
			t.Errorf("blank continuation line under the absent device, got:\n%s", out)
		}
	}
}

func TestFormatListDisclosesWhereTheListCameFrom(t *testing.T) {
	// A configured list replaces the built-ins, so someone who added one device
	// by hand has to be able to see why the rest disappeared.
	var builtin, configured bytes.Buffer
	FormatList(&builtin, testDevices(), false, false)
	FormatList(&configured, testDevices(), false, true)

	if !strings.Contains(builtin.String(), "Built-in") {
		t.Errorf("built-in listing does not say so:\n%s", builtin.String())
	}
	if !strings.Contains(configured.String(), "config") {
		t.Errorf("configured listing does not say so:\n%s", configured.String())
	}
	if builtin.String() == configured.String() {
		t.Error("the two listings are identical — where the list came from is invisible")
	}
}

func TestParseHexID(t *testing.T) {
	ok := map[string]uint16{
		"0x30B7": 0x30B7, "30B7": 0x30B7, "30b7": 0x30B7,
		"VID_30B7": 0x30B7, "PID_1001": 0x1001, "  0x1 ": 1, "FFFF": 0xFFFF,
	}
	for in, want := range ok {
		got, err := ParseHexID(in)
		if err != nil {
			t.Errorf("ParseHexID(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseHexID(%q) = %#x, want %#x", in, got, want)
		}
	}

	for _, in := range []string{"", "  ", "zzz", "0x10000", "12345", "-1", "0x"} {
		if _, err := ParseHexID(in); err == nil {
			t.Errorf("ParseHexID(%q) accepted a bad ID", in)
		}
	}
}

func TestFormatHexIDRoundTrips(t *testing.T) {
	for _, v := range []uint16{0, 1, 0x30B7, 0xFFFF} {
		got, err := ParseHexID(FormatHexID(v))
		if err != nil || got != v {
			t.Errorf("round trip of %#x gave %#x, %v", v, got, err)
		}
	}
}

// A non-empty config list replaces the built-ins rather than adding to them, so
// a device you do not own can be dropped. An empty one falls back.
func TestResolveKnown(t *testing.T) {
	if got := ResolveKnown(nil); len(got) != len(KnownDevices) {
		t.Errorf("ResolveKnown(nil) = %d devices, want the %d built-ins", len(got), len(KnownDevices))
	}

	mine := []Known{{Alias: "shifter", Name: "Q1", VID: 1, PID: 2}}
	got := ResolveKnown(mine)
	if len(got) != 1 || got[0].Alias != "shifter" {
		t.Fatalf("ResolveKnown(mine) = %+v, want only the configured device", got)
	}

	// The returned slice must not alias the caller's, or a later append here
	// would reach into the config's own list.
	got[0].Alias = "mutated"
	if mine[0].Alias != "shifter" {
		t.Error("ResolveKnown returned a slice aliasing its input")
	}
	if ResolveKnown(nil)[0].Alias == "mutated" {
		t.Error("ResolveKnown returned a slice aliasing the package's built-in list")
	}
}

func TestScannedHardwareID(t *testing.T) {
	s := Scanned{VID: 0x30B7, PID: 0x1001}
	if got := s.HardwareID(); got != "VID_30B7&PID_1001" {
		t.Errorf("HardwareID = %q, want the Device Manager form", got)
	}
	if s.IsKnown() {
		t.Error("a device with no alias reported as known")
	}
	s.Alias = "pedals"
	if !s.IsKnown() {
		t.Error("a device with an alias reported as unknown")
	}
}

// The picker sorts claimed devices first, then by hardware ID, because
// enumeration order follows the PnP tree and shuffles with which port the
// hardware went into.
func TestSortScanned(t *testing.T) {
	found := []Scanned{
		{VID: 0xFFFF, PID: 0x0001},
		{VID: 0x0001, PID: 0x0002, Alias: "wheelbase"},
		{VID: 0x000A, PID: 0x0002},
		{VID: 0x0002, PID: 0x0003, Alias: "pedals"},
	}
	SortScanned(found)

	if found[0].Alias != "pedals" || found[1].Alias != "wheelbase" {
		t.Fatalf("claimed devices not first and alias-sorted: %+v", found)
	}
	if found[2].HardwareID() >= found[3].HardwareID() {
		t.Errorf("unclaimed devices not sorted by hardware ID: %q then %q",
			found[2].HardwareID(), found[3].HardwareID())
	}
}

func TestFormatScan(t *testing.T) {
	var buf bytes.Buffer
	FormatScan(&buf, []Scanned{
		{VID: 0x30B7, PID: 0x1001, Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint", State: StateEnabled},
		{VID: 0x046D, PID: 0xC52B, Desc: "Logitech Receiver", State: StateEnabled},
	})
	out := buf.String()

	for _, want := range []string{"VID_30B7&PID_1001", "pedals", "VID_046D&PID_C52B", "Logitech Receiver"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the scan, got:\n%s", want, out)
		}
	}
	// An unclaimed device shows a dash rather than a blank, so the column does
	// not read as a missing value.
	if !strings.Contains(out, "-") {
		t.Errorf("unclaimed device has no alias placeholder:\n%s", out)
	}
}

func TestIsHubServiceName(t *testing.T) {
	// Hubs are the bulk of a USB walk and none of them is a sim control.
	for _, s := range []string{"USBHUB", "usbhub3", "USBHUB3", "UsbHub", "USBXHCI", "usbehci", " USBHUB "} {
		if !IsHubServiceName(s) {
			t.Errorf("IsHubServiceName(%q) = false, want true", s)
		}
	}
	// Everything a peripheral is actually backed by must survive the filter —
	// usbccgp in particular, which is what a composite device like the MOZA
	// handbrake uses.
	for _, s := range []string{"", "HidUsb", "usbccgp", "WinUSB", "usbser", "USBSTOR", "hub-ish"} {
		if IsHubServiceName(s) {
			t.Errorf("IsHubServiceName(%q) = true, want false", s)
		}
	}
}

// A hardware ID shared by several devnodes is one row with a count, because a
// device-list entry matches by VID/PID and would claim all of them.
func TestFormatScanDisclosesSharedHardwareIDs(t *testing.T) {
	var buf bytes.Buffer
	FormatScan(&buf, []Scanned{
		{VID: 0x046D, PID: 0xC547, Desc: "LIGHTSPEED Receiver", State: StateEnabled, Count: 8},
	})
	out := buf.String()

	if !strings.Contains(out, "8 devices share this ID") {
		t.Errorf("shared hardware ID not disclosed on the row:\n%s", out)
	}
	if !strings.Contains(out, "claims every device with it") {
		t.Errorf("consequence of adding a shared ID not explained:\n%s", out)
	}
}

func TestFormatScanOmitsCountForSingletons(t *testing.T) {
	var buf bytes.Buffer
	FormatScan(&buf, []Scanned{
		{VID: 0x30B7, PID: 0x1001, Alias: "pedals", Name: "Pedals", State: StateEnabled, Count: 1},
	})
	out := buf.String()

	if strings.Contains(out, "share this ID") {
		t.Errorf("a single device was annotated as shared:\n%s", out)
	}
	// The footer warning is only relevant when something is actually shared.
	if strings.Contains(out, "claims every device with it") {
		t.Errorf("shared-ID warning shown with no shared IDs:\n%s", out)
	}
}
