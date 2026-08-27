package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rickymw/MotorHome/internal/usbdev"
)

// usbOut and usbErrOut are the sinks for everything the usb subcommand prints.
//
// They exist because the elevated re-exec cannot print to the terminal: a
// process launched with the "runas" verb gets its own console, which the parent
// has no handle to. The child redirects both sinks to a file that the parent
// reads back and replays, so a toggle looks identical whether or not it had to
// elevate. Tests point them at a buffer.
var (
	usbOut    io.Writer = os.Stdout
	usbErrOut io.Writer = os.Stderr

	// Swappable for tests, which have no rig attached and must not elevate.
	newUSBController = usbdev.NewController
	usbIsElevated    = winIsElevated
	usbRelaunch      = winRelaunchElevated
)

func usbPrintf(format string, a ...any) { fmt.Fprintf(usbOut, format, a...) }
func usbErrf(format string, a ...any)   { fmt.Fprintf(usbErrOut, format+"\n", a...) }

func isUSBAction(s string) bool {
	switch s {
	case "list", "on", "off", "toggle":
		return true
	}
	return false
}

// RunUSB lists the sim-racing USB devices and enables or disables them,
// returning the process exit code.
//
// Listing needs no special rights; changing a device state does, so RunUSB
// re-runs itself elevated for that half only. Enumeration and target resolution
// stay in the unelevated parent so a typo ("no device matches pedls") is
// reported without an elevation round trip.
func RunUSB(args []string) int {
	action := "list"
	rest := args
	if len(args) > 0 && isUSBAction(args[0]) {
		action, rest = args[0], args[1:]
	}

	fs := flag.NewFlagSet("usb "+action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	verbose := fs.Bool("v", false, "show device instance IDs")
	elevatedOut := fs.String("elevated-out", "", "internal: redirect all output to this file (set by the elevated re-exec)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: motorhome usb [list] [-v]")
		fmt.Fprintln(os.Stderr, "       motorhome usb <on|off|toggle> [-v] <alias|all>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}

	if *elevatedOut != "" {
		f, err := os.Create(*elevatedOut)
		if err != nil {
			// Nothing can be reported from here — the parent is reading that
			// file and this console is invisible — so only the exit code lands.
			return 1
		}
		defer f.Close()
		usbOut, usbErrOut = f, f
	}

	ctrl := newUSBController()
	devs, err := ctrl.Enumerate()
	if err != nil {
		usbErrf("cannot enumerate USB devices: %v", err)
		return 1
	}

	if action == "list" {
		usbdev.FormatList(usbOut, devs, *verbose)
		return 0
	}

	target := fs.Arg(0)
	if target == "" {
		usbErrf("usb %s: no device named", action)
		usbdev.FormatList(usbErrOut, devs, false)
		return 1
	}

	targets, err := usbdev.Resolve(devs, target)
	if err != nil {
		usbErrf("usb %s: %v", action, err)
		return 1
	}

	// Checked here rather than inside usbApply so a target that is entirely
	// unplugged fails immediately instead of paying for an elevation first.
	if !anyActionable(targets) {
		usbErrf("nothing to do — no matching device is connected")
		return 1
	}

	if *elevatedOut == "" && !usbIsElevated() {
		return usbElevate(action, target, *verbose)
	}

	return usbApply(ctrl, action, targets)
}

// anyActionable reports whether any target has a devnode to act on.
func anyActionable(devs []usbdev.Device) bool {
	for _, d := range devs {
		if d.State != usbdev.StateAbsent {
			return true
		}
	}
	return false
}

// usbElevate re-runs this subcommand under an elevated token and replays what
// it printed.
//
// The child's arguments are rebuilt rather than forwarded verbatim so the
// flags land ahead of the target regardless of how they were typed.
func usbElevate(action, target string, verbose bool) int {
	tmp, err := os.CreateTemp("", "motorhome-usb-*.txt")
	if err != nil {
		usbErrf("cannot create temp file for elevated output: %v", err)
		return 1
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	childArgs := []string{"usb", action, "-elevated-out", path}
	if verbose {
		childArgs = append(childArgs, "-v")
	}
	childArgs = append(childArgs, target)

	code, err := usbRelaunch(childArgs)
	if err != nil {
		usbErrf("%v", err)
		usbErrf("changing a device state needs administrator rights — run this from an elevated prompt instead")
		return 1
	}

	if out, readErr := os.ReadFile(path); readErr == nil && len(out) > 0 {
		usbOut.Write(out)
	}
	return code
}

// usbApply performs the state changes. It runs only in the process that holds
// the elevated token, which is also why it — and not the caller that may be
// about to hand off — owns every line of per-device output: printing in both
// would show each device twice, once from the parent and once replayed from
// the elevated child.
func usbApply(ctrl usbdev.Controller, action string, devs []usbdev.Device) int {
	failed := 0
	changed := 0

	for _, d := range devs {
		// A device that is not plugged in has no devnode to toggle. Report and
		// skip it rather than failing, so `usb off all` still works with the
		// wheelbase off the rig — its normal state between sessions.
		if d.State == usbdev.StateAbsent {
			usbPrintf("  [=] %-11s %-32s not connected\n", d.Alias, d.Name)
			continue
		}

		enable := action == "on"
		if action == "toggle" {
			enable = d.State == usbdev.StateDisabled
		}

		if (enable && d.State == usbdev.StateEnabled) || (!enable && d.State == usbdev.StateDisabled) {
			usbPrintf("  [=] %-11s %-32s already %s\n", d.Alias, d.Name, d.State)
			continue
		}

		needsRestart, err := ctrl.SetEnabled(d.InstanceID, enable)
		if err != nil {
			usbPrintf("  [!] %-11s %-32s FAILED: %v\n", d.Alias, d.Name, err)
			failed++
			continue
		}

		verb := "disabled"
		if enable {
			verb = "enabled"
		}
		usbPrintf("  [+] %-11s %-32s %s\n", d.Alias, d.Name, verb)
		changed++
		if needsRestart {
			usbPrintf("      Windows reports a restart is needed before this takes effect\n")
		}
	}

	if failed > 0 {
		return 1
	}
	if changed > 0 {
		usbPrintf("\nA game that enumerated its controllers at startup may need restarting to notice.\n")
	}
	return 0
}

// usbTargetHint is used by the usage text to name the aliases without needing a
// rig attached.
func usbTargetHint() string {
	names := make([]string, 0, len(usbdev.KnownDevices)+1)
	for _, k := range usbdev.KnownDevices {
		names = append(names, k.Alias)
	}
	names = append(names, "all")
	return strings.Join(names, "|")
}
