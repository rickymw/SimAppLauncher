// Package usbdev identifies the sim-racing USB devices attached to this machine
// and enables or disables them individually.
//
// The problem it solves: every device on the rig — wheelbase, pedals, handbrake,
// haptic unit — presents to Windows as a HID game controller. A game that
// enumerates all controllers and auto-binds axes picks up phantom input from the
// ones that aren't being used. That is harmless in a sim, which expects several
// devices, and disruptive in anything else.
//
// Devices are matched by USB vendor/product ID rather than by name, because the
// name Windows reports for all of them is the generic "USB Input Device" — the
// real product string lives in a bus-reported property that the device itself
// supplies, and is not what the PnP enumerator keys on.
package usbdev

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// State is what a known device is currently doing.
type State int

const (
	// StateAbsent means the device is not plugged in. It is distinct from
	// StateDisabled: a disabled device still has a devnode and can be
	// re-enabled, an absent one cannot be acted on at all.
	StateAbsent State = iota
	StateEnabled
	StateDisabled
)

func (s State) String() string {
	switch s {
	case StateEnabled:
		return "enabled"
	case StateDisabled:
		return "disabled"
	default:
		return "not connected"
	}
}

// Known is a sim-racing device this tool can recognise.
type Known struct {
	Alias string // short name used on the command line
	Name  string // human-readable product name
	VID   uint16
	PID   uint16
}

// KnownDevices is the rig. Entries are matched against the top-level USB
// device node, never an interface node — see parseVIDPID.
//
// The wheelbase VID (0x0483) belongs to STMicroelectronics rather than SIMAGIC,
// since the base uses an ST microcontroller and never overrode the default; the
// PID is what actually pins it down.
var KnownDevices = []Known{
	{Alias: "wheelbase", Name: "SIMAGIC Alpha Series Wheelbase", VID: 0x0483, PID: 0x0522},
	{Alias: "haptic", Name: "SIMAGIC P2000 Haptic", VID: 0x3670, PID: 0x0902},
	{Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint", VID: 0x30B7, PID: 0x1001},
	{Alias: "handbrake", Name: "MOZA HBP Handbrake", VID: 0x346E, PID: 0x001F},
}

// Device is a known device plus whatever the machine currently reports for it.
type Device struct {
	Known
	InstanceID string // empty when State is StateAbsent
	Desc       string // the description Windows reports, for diagnostics
	State      State
}

// Controller enumerates and toggles devices. It is an interface so the command
// layer can be tested without a rig plugged in, and so the Win32 half stays in
// one Windows-only file.
type Controller interface {
	Enumerate() ([]Device, error)
	// SetEnabled enables or disables one device, reporting whether Windows
	// asked for a restart before the change takes effect.
	SetEnabled(instanceID string, enable bool) (needsRestart bool, err error)
}

// parseVIDPID extracts the vendor and product IDs from a USB instance ID such
// as `USB\VID_30B7&PID_1001\SP24776C025F602628`.
//
// Interface nodes (those carrying &MI_) are rejected. A composite device like
// the MOZA handbrake publishes one node per USB interface — a serial port and a
// game controller — beneath a single top-level device node. Toggling the
// top-level node is what "turn this device off" means; toggling one interface
// would leave the physical device half-on in a way nothing in the output could
// sensibly describe.
func parseVIDPID(instanceID string) (vid, pid uint16, ok bool) {
	parts := strings.Split(instanceID, `\`)
	if len(parts) < 2 || !strings.EqualFold(parts[0], "USB") {
		return 0, 0, false
	}
	id := strings.ToUpper(parts[1])
	if strings.Contains(id, "&MI_") {
		return 0, 0, false
	}

	var haveVID, havePID bool
	for _, field := range strings.Split(id, "&") {
		switch {
		case strings.HasPrefix(field, "VID_"):
			if v, err := strconv.ParseUint(field[4:], 16, 16); err == nil {
				vid, haveVID = uint16(v), true
			}
		case strings.HasPrefix(field, "PID_"):
			if p, err := strconv.ParseUint(field[4:], 16, 16); err == nil {
				pid, havePID = uint16(p), true
			}
		}
	}
	return vid, pid, haveVID && havePID
}

// match returns the known device for a USB instance ID, if it is one of ours.
func match(instanceID string) (Known, bool) {
	vid, pid, ok := parseVIDPID(instanceID)
	if !ok {
		return Known{}, false
	}
	for _, k := range KnownDevices {
		if k.VID == vid && k.PID == pid {
			return k, true
		}
	}
	return Known{}, false
}

// Resolve turns a command-line target into the devices it names.
//
// Like `pb show`, it refuses to guess: a target matching several devices is an
// error listing them rather than a pick. Disabling the wrong device mid-session
// is cheap to undo but not cheap to notice, since the symptom is a control that
// silently stopped working.
func Resolve(devs []Device, target string) ([]Device, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("no device named")
	}

	// "all" deliberately includes devices that are not plugged in. Dropping
	// them here would make `usb off all` silently omit the wheelbase, leaving
	// no way to tell "it is off the rig" from "the tool does not know about
	// it". The caller reports and skips them, so the listing stays honest.
	if strings.EqualFold(target, "all") {
		if len(devs) == 0 {
			return nil, fmt.Errorf("no sim racing devices are known")
		}
		return append([]Device(nil), devs...), nil
	}

	for _, d := range devs {
		if strings.EqualFold(d.Alias, target) {
			return []Device{d}, nil
		}
	}

	var hits []Device
	lower := strings.ToLower(target)
	for _, d := range devs {
		if strings.Contains(strings.ToLower(d.Alias), lower) ||
			strings.Contains(strings.ToLower(d.Name), lower) {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 1:
		return hits, nil
	case 0:
		return nil, fmt.Errorf("no device matches %q\nknown devices: %s", target, aliasList(devs))
	default:
		var names []string
		for _, d := range hits {
			names = append(names, fmt.Sprintf("%s (%s)", d.Alias, d.Name))
		}
		return nil, fmt.Errorf("%q matches %d devices: %s\nbe more specific", target, len(hits), strings.Join(names, ", "))
	}
}

func aliasList(devs []Device) string {
	var out []string
	for _, d := range devs {
		out = append(out, d.Alias)
	}
	out = append(out, "all")
	return strings.Join(out, ", ")
}

// SortDevices orders devices by alias so listings don't shuffle between runs —
// enumeration order follows the PnP tree, which changes with which ports the
// hardware was plugged into.
func SortDevices(devs []Device) {
	sort.Slice(devs, func(i, j int) bool { return devs[i].Alias < devs[j].Alias })
}

// FormatList writes the device table.
func FormatList(w io.Writer, devs []Device, verbose bool) {
	fmt.Fprintln(w, "Sim racing USB devices")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-11s %-14s %s\n", "Alias", "State", "Device")
	for _, d := range devs {
		fmt.Fprintf(w, "  %-11s %-14s %s\n", d.Alias, d.State, d.Name)
		if verbose && d.InstanceID != "" {
			fmt.Fprintf(w, "  %-11s %-14s %s\n", "", "", d.InstanceID)
		}
	}
}
