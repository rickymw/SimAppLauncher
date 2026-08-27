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

// KnownDevices is this rig as shipped, used when the config names no devices of
// its own. Entries are matched against the top-level USB device node, never an
// interface node — see parseVIDPID.
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

// Scanned is one top-level USB device as the machine reports it, whether or not
// it is one of ours.
//
// This is what makes a device pickable instead of hand-entered. Enumerate has
// always walked every USB device and thrown away the ones that did not match a
// known VID/PID; the only thing separating that walk from a "which of these is
// your pedals?" list was the discard. Finding a VID/PID by hand — digging it out
// of Device Manager or PowerShell — was the genuinely awkward part of adding a
// device, and it is the part this removes.
type Scanned struct {
	InstanceID string
	Desc       string // the description Windows reports, e.g. "USB Input Device"
	VID        uint16
	PID        uint16
	State      State

	// Alias and Name are set when this device is already in the known list,
	// so a picker can show what is already claimed rather than offering it
	// again as if it were new.
	Alias string
	Name  string

	// Count is how many devnodes share this VID/PID. It is usually 1, and the
	// exceptions matter: this rig reports eight identical wireless receivers
	// under one hardware ID. Since a device-list entry selects by VID/PID, all
	// of them would be claimed by a single entry — and `usb off` would act on
	// only one. Surfacing the count is what stops that being a silent surprise.
	Count int
}

// IsHubServiceName reports whether a Windows driver service name belongs to a
// USB hub or host controller.
//
// Hubs are the bulk of what a USB enumeration returns — 27 of 63 nodes on this
// rig — none of them is a sim control, and disabling one would take down
// everything plugged into it. Excluding them is what turns a scan into a list
// somebody can read.
//
// It keys on the service rather than the description because the description is
// localised: "Generic USB Hub" is that string only on an English Windows, while
// the service is a driver identifier and is not translated.
func IsHubServiceName(service string) bool {
	s := strings.ToUpper(strings.TrimSpace(service))
	if strings.HasPrefix(s, "USBHUB") {
		return true
	}
	switch s {
	case "USBXHCI", "USBEHCI", "USBOHCI", "USBUHCI", "USBHUB3":
		return true
	}
	return false
}

// Known reports whether this device is already in the configured list.
func (s Scanned) IsKnown() bool { return s.Alias != "" }

// HardwareID renders the VID/PID the way Windows writes it, which is how it
// appears in Device Manager and therefore how someone cross-checking will
// recognise it.
func (s Scanned) HardwareID() string {
	return fmt.Sprintf("VID_%04X&PID_%04X", s.VID, s.PID)
}

// Controller enumerates and toggles devices. It is an interface so the command
// layer can be tested without a rig plugged in, and so the Win32 half stays in
// one Windows-only file.
type Controller interface {
	Enumerate() ([]Device, error)
	// Scan reports every top-level USB device attached to the machine,
	// including ones not in the known list.
	Scan() ([]Scanned, error)
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

// match returns the entry in known for a USB instance ID, if it is one of ours.
//
// The list is a parameter rather than the package global because it now comes
// from the user's config; keeping it global would mean the answer depended on
// process-wide state that a test — or a second controller — could not vary.
func match(known []Known, instanceID string) (Known, bool) {
	vid, pid, ok := parseVIDPID(instanceID)
	if !ok {
		return Known{}, false
	}
	for _, k := range known {
		if k.VID == vid && k.PID == pid {
			return k, true
		}
	}
	return Known{}, false
}

// Resolve turns a config-supplied device list into the one the controller
// should use, falling back to the built-in rig when the config names none.
//
// A non-empty config list **replaces** the built-ins rather than adding to
// them. Additive would be the safer-sounding rule, but it makes one thing
// impossible: dropping a device you do not own. A rig with no haptic unit would
// carry a phantom "not connected" row forever with no way to remove it, and
// "not connected" is supposed to mean "unplugged right now", not "belongs to
// somebody else".
//
// The cost of that choice is that hand-adding one device to the config silently
// drops the other three. It is not silent: callers report which list is in use
// (see FormatList), and the GUI seeds the full built-in set when it writes the
// first entry, so the ordinary path never meets the sharp edge.
func ResolveKnown(fromConfig []Known) []Known {
	if len(fromConfig) == 0 {
		return append([]Known(nil), KnownDevices...)
	}
	return append([]Known(nil), fromConfig...)
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
//
// fromConfig says the list came from launcher.config.json rather than the
// built-in defaults, and is disclosed rather than inferred: because a configured
// list replaces the built-ins, someone who added one device by hand needs to be
// able to see why the other three vanished. A table that looked identical either
// way would make that a mystery.
func FormatList(w io.Writer, devs []Device, verbose, fromConfig bool) {
	fmt.Fprintln(w, "Sim racing USB devices")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-11s %-14s %s\n", "Alias", "State", "Device")
	for _, d := range devs {
		fmt.Fprintf(w, "  %-11s %-14s %s\n", d.Alias, d.State, d.Name)
		if verbose && d.InstanceID != "" {
			fmt.Fprintf(w, "  %-11s %-14s %s\n", "", "", d.InstanceID)
		}
	}
	fmt.Fprintln(w)
	if fromConfig {
		fmt.Fprintf(w, "  %d device(s) from usbDevices in the config.\n", len(devs))
		return
	}
	fmt.Fprintln(w, "  Built-in device list. Add your own with `motorhome gui` → Rig → Scan.")
}

// FormatScan writes the results of a scan: every top-level USB device on the
// machine, with the ones already claimed marked.
func FormatScan(w io.Writer, found []Scanned) {
	fmt.Fprintln(w, "USB devices attached to this machine")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-22s %-14s %-11s %s\n", "Hardware ID", "State", "Alias", "Description")
	dupes := false
	for _, s := range found {
		alias := s.Alias
		if alias == "" {
			alias = "-"
		}
		desc := s.Desc
		if s.Name != "" {
			desc = s.Name
		}
		// A count above one means several devnodes share this hardware ID, and
		// one list entry would claim all of them. Said on the row rather than
		// only in a footer, because it changes what adding that row means.
		if s.Count > 1 {
			desc = fmt.Sprintf("%s  (%d devices share this ID)", desc, s.Count)
			dupes = true
		}
		fmt.Fprintf(w, "  %-22s %-14s %-11s %s\n", s.HardwareID(), s.State, alias, desc)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Devices with no alias are not in your device list.")
	fmt.Fprintln(w, "  Add one with `motorhome gui` → Rig → Scan, which fills in the IDs for you.")
	fmt.Fprintln(w, "  USB hubs are not shown.")
	if dupes {
		fmt.Fprintln(w, "  Devices are matched by hardware ID, so adding a shared one claims every device with it.")
	}
}

// SortScanned orders a scan so the devices already claimed come first, then by
// hardware ID. Enumeration order follows the PnP tree, which shuffles with which
// port the hardware went into — no use to someone reading a list to find their
// pedals.
func SortScanned(found []Scanned) {
	sort.Slice(found, func(i, j int) bool {
		if found[i].IsKnown() != found[j].IsKnown() {
			return found[i].IsKnown()
		}
		if found[i].IsKnown() {
			return found[i].Alias < found[j].Alias
		}
		return found[i].HardwareID() < found[j].HardwareID()
	})
}

// ParseHexID reads a VID or PID written the way people find them: "0x30B7",
// "30B7", or "VID_30B7". Case-insensitive.
//
// It accepts the prefixed forms because that is how the ID appears in Device
// Manager and in an instance ID string, and someone copying one across should
// not have to know which part to strip.
func ParseHexID(s string) (uint16, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, fmt.Errorf("empty")
	}
	upper := strings.ToUpper(t)
	for _, prefix := range []string{"VID_", "PID_", "0X"} {
		upper = strings.TrimPrefix(upper, prefix)
	}
	v, err := strconv.ParseUint(upper, 16, 16)
	if err != nil {
		return 0, fmt.Errorf("%q is not a 4-digit hex ID", s)
	}
	return uint16(v), nil
}

// FormatHexID renders an ID back in the form the config stores.
func FormatHexID(v uint16) string { return fmt.Sprintf("0x%04X", v) }
