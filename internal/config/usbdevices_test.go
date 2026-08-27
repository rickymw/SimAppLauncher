package config

import (
	"strings"
	"testing"
)

func withDevices(devs ...USBDevice) Config {
	return Config{USBDevices: devs}
}

func TestValidateAcceptsAWellFormedDevice(t *testing.T) {
	cfg := withDevices(USBDevice{Alias: "shifter", Name: "SIMAGIC Q1", VID: "0x3670", PID: "0x0401"})
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	known := cfg.KnownUSBDevices()
	if len(known) != 1 {
		t.Fatalf("got %d known devices, want 1", len(known))
	}
	if known[0].VID != 0x3670 || known[0].PID != 0x0401 {
		t.Errorf("parsed IDs = %#x/%#x, want 0x3670/0x0401", known[0].VID, known[0].PID)
	}
}

// The IDs are accepted in every form someone might copy one in: Device Manager
// shows VID_30B7, a hex literal is 0x30B7, and the bare digits are what a table
// of hardware IDs prints.
func TestValidateAcceptsEveryHexIDForm(t *testing.T) {
	for _, form := range []string{"0x30B7", "30B7", "30b7", "VID_30B7", "  0x30B7  "} {
		cfg := withDevices(USBDevice{Alias: "pedals", Name: "Pedals", VID: form, PID: "0x1001"})
		if err := cfg.Validate(); err != nil {
			t.Errorf("VID %q rejected: %v", form, err)
			continue
		}
		if got := cfg.KnownUSBDevices()[0].VID; got != 0x30B7 {
			t.Errorf("VID %q parsed as %#x, want 0x30B7", form, got)
		}
	}
}

func TestValidateRejectsBadDevices(t *testing.T) {
	cases := []struct {
		name string
		dev  USBDevice
		want string
	}{
		{"no alias", USBDevice{Name: "X", VID: "0x1", PID: "0x2"}, "alias is required"},
		{"no name", USBDevice{Alias: "x", VID: "0x1", PID: "0x2"}, "name is required"},
		{"bad vid", USBDevice{Alias: "x", Name: "X", VID: "zzzz", PID: "0x2"}, "vid"},
		{"bad pid", USBDevice{Alias: "x", Name: "X", VID: "0x1", PID: ""}, "pid"},
		{"vid too wide", USBDevice{Alias: "x", Name: "X", VID: "0x12345", PID: "0x2"}, "vid"},
		// The alias reaches a command line, where a leading dash is a flag.
		{"dash alias", USBDevice{Alias: "-v", Name: "X", VID: "0x1", PID: "0x2"}, "must not start with a dash"},
		{"padded alias", USBDevice{Alias: " x ", Name: "X", VID: "0x1", PID: "0x2"}, "whitespace"},
		// "all" is the wildcard target, so a device named that could never be
		// addressed on its own.
		{"reserved alias", USBDevice{Alias: "all", Name: "X", VID: "0x1", PID: "0x2"}, "reserved"},
		{"reserved alias, cased", USBDevice{Alias: "All", Name: "X", VID: "0x1", PID: "0x2"}, "reserved"},
	}
	for _, c := range cases {
		err := withDevices(c.dev).Validate()
		if err == nil {
			t.Errorf("%s: accepted %+v", c.name, c.dev)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not mention %q", c.name, err, c.want)
		}
	}
}

func TestValidateRejectsDuplicateAlias(t *testing.T) {
	err := withDevices(
		USBDevice{Alias: "pedals", Name: "A", VID: "0x1", PID: "0x2"},
		USBDevice{Alias: "Pedals", Name: "B", VID: "0x3", PID: "0x4"},
	).Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate alias") {
		t.Fatalf("err = %v, want a duplicate-alias rejection (aliases match case-insensitively)", err)
	}
}

// Two aliases on one VID/PID would both match the same physical device, so
// either name would toggle it and the listing would show it twice in states
// that could disagree.
func TestValidateRejectsDuplicateHardwareID(t *testing.T) {
	err := withDevices(
		USBDevice{Alias: "pedals", Name: "A", VID: "0x30B7", PID: "0x1001"},
		USBDevice{Alias: "feet", Name: "B", VID: "30b7", PID: "1001"},
	).Validate()
	if err == nil || !strings.Contains(err.Error(), "two aliases") {
		t.Fatalf("err = %v, want a duplicate vid/pid rejection across differing hex forms", err)
	}
}

// An empty list is the normal case for an existing config and must stay valid —
// it means "use the built-in device list".
func TestValidateAcceptsNoDevices(t *testing.T) {
	if err := (Config{}).Validate(); err != nil {
		t.Fatalf("a config with no usbDevices was rejected: %v", err)
	}
	if got := (Config{}).KnownUSBDevices(); len(got) != 0 {
		t.Errorf("KnownUSBDevices = %+v, want empty so usbdev falls back to built-ins", got)
	}
}
