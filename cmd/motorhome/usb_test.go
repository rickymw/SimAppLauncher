package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/usbdev"
)

type fakeUSBController struct {
	devs     []usbdev.Device
	enumErr  error
	setErr   error
	restart  bool
	setCalls []fakeSetCall
	scanned  []usbdev.Scanned
	gotKnown []usbdev.Known
}

type fakeSetCall struct {
	instanceID string
	enable     bool
}

func (f *fakeUSBController) Enumerate() ([]usbdev.Device, error) {
	if f.enumErr != nil {
		return nil, f.enumErr
	}
	return f.devs, nil
}

func (f *fakeUSBController) Scan() ([]usbdev.Scanned, error) {
	if f.enumErr != nil {
		return nil, f.enumErr
	}
	return f.scanned, nil
}

func (f *fakeUSBController) SetEnabled(instanceID string, enable bool) (bool, error) {
	f.setCalls = append(f.setCalls, fakeSetCall{instanceID, enable})
	if f.setErr != nil {
		return false, f.setErr
	}
	return f.restart, nil
}

func fakeUSBDevices() []usbdev.Device {
	return []usbdev.Device{
		{Known: usbdev.Known{Alias: "handbrake", Name: "MOZA HBP Handbrake"}, InstanceID: `USB\A`, State: usbdev.StateEnabled},
		{Known: usbdev.Known{Alias: "haptic", Name: "SIMAGIC P2000 Haptic"}, InstanceID: `USB\B`, State: usbdev.StateDisabled},
		{Known: usbdev.Known{Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint"}, InstanceID: `USB\C`, State: usbdev.StateEnabled},
		{Known: usbdev.Known{Alias: "wheelbase", Name: "SIMAGIC Alpha Series Wheelbase"}, State: usbdev.StateAbsent},
	}
}

// runUSB drives RunUSB with a fake controller, returning stdout, stderr and the
// exit code. elevated controls whether the command believes it can act directly
// or must hand off to a re-exec.
//
// The config path points at a file that does not exist, which is the built-in
// device list case — the same thing a bare copy of the exe sees.
func runUSB(t *testing.T, ctrl usbdev.Controller, elevated bool, relaunch func([]string) (int, error), args ...string) (string, string, int) {
	t.Helper()
	return runUSBWithConfig(t, filepath.Join(t.TempDir(), "launcher.config.json"),
		ctrl, elevated, relaunch, args...)
}

// runUSBWithConfig is runUSB against a named config file, for the cases where
// what the config says is the thing under test.
func runUSBWithConfig(t *testing.T, cfgPath string, ctrl usbdev.Controller, elevated bool, relaunch func([]string) (int, error), args ...string) (string, string, int) {
	t.Helper()

	oldOut, oldErr := usbOut, usbErrOut
	oldCtrl, oldElev, oldRelaunch := newUSBController, usbIsElevated, usbRelaunch
	t.Cleanup(func() {
		usbOut, usbErrOut = oldOut, oldErr
		newUSBController, usbIsElevated, usbRelaunch = oldCtrl, oldElev, oldRelaunch
	})

	var out, errBuf bytes.Buffer
	usbOut, usbErrOut = &out, &errBuf
	newUSBController = func(known []usbdev.Known) usbdev.Controller {
		if f, ok := ctrl.(*fakeUSBController); ok {
			f.gotKnown = known
		}
		return ctrl
	}
	usbIsElevated = func() bool { return elevated }
	usbRelaunch = relaunch
	if relaunch == nil {
		usbRelaunch = func([]string) (int, error) {
			t.Fatal("did not expect an elevation attempt")
			return 0, nil
		}
	}

	code := RunUSB(args, cfgPath)
	return out.String(), errBuf.String(), code
}

// writeUSBConfig writes a config naming devs and returns its path.
func writeUSBConfig(t *testing.T, devs ...config.USBDevice) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "launcher.config.json")
	if err := config.Save(path, config.Config{Driver: "Ricky Maw", USBDevices: devs}); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestRunUSBList(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	out, _, code := runUSB(t, ctrl, true, nil)

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	for _, want := range []string{"handbrake", "pedals", "disabled", "not connected"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in listing, got:\n%s", want, out)
		}
	}
	if len(ctrl.setCalls) != 0 {
		t.Errorf("listing must not change any device state, got %v", ctrl.setCalls)
	}
}

func TestRunUSBListIsTheDefaultAction(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	bare, _, _ := runUSB(t, ctrl, true, nil)
	explicit, _, _ := runUSB(t, ctrl, true, nil, "list")
	if bare != explicit {
		t.Errorf("bare `usb` and `usb list` should render identically:\n%q\nvs\n%q", bare, explicit)
	}
}

func TestRunUSBOff(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	out, _, code := runUSB(t, ctrl, true, nil, "off", "pedals")

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if len(ctrl.setCalls) != 1 {
		t.Fatalf("expected one state change, got %v", ctrl.setCalls)
	}
	if ctrl.setCalls[0].instanceID != `USB\C` || ctrl.setCalls[0].enable {
		t.Errorf("got %+v, want a disable of the pedals", ctrl.setCalls[0])
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected confirmation in output, got:\n%s", out)
	}
}

// TestRunUSBOffAlreadyDisabled asserts a no-op is reported as one rather than
// claiming a change that did not happen.
func TestRunUSBOffAlreadyDisabled(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	out, _, code := runUSB(t, ctrl, true, nil, "off", "haptic")

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if len(ctrl.setCalls) != 0 {
		t.Errorf("expected no state change, got %v", ctrl.setCalls)
	}
	if !strings.Contains(out, "already disabled") {
		t.Errorf("expected `already disabled`, got:\n%s", out)
	}
}

func TestRunUSBToggleUsesCurrentState(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	_, _, code := runUSB(t, ctrl, true, nil, "toggle", "all")

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	want := map[string]bool{`USB\A`: false, `USB\B`: true, `USB\C`: false}
	if len(ctrl.setCalls) != len(want) {
		t.Fatalf("expected %d state changes, got %v", len(want), ctrl.setCalls)
	}
	for _, call := range ctrl.setCalls {
		if enable, ok := want[call.instanceID]; !ok || enable != call.enable {
			t.Errorf("%s: enable = %v, want %v", call.instanceID, call.enable, want[call.instanceID])
		}
	}
}

func TestRunUSBAllSkipsAbsentDevice(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	out, _, code := runUSB(t, ctrl, true, nil, "off", "all")

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	for _, call := range ctrl.setCalls {
		if call.instanceID == "" {
			t.Error("attempted to toggle a device with no devnode")
		}
	}
	if !strings.Contains(out, "not connected") {
		t.Errorf("expected the absent wheelbase disclosed, got:\n%s", out)
	}
}

func TestRunUSBAbsentTargetFails(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	_, errOut, code := runUSB(t, ctrl, true, nil, "off", "wheelbase")

	if code == 0 {
		t.Error("expected a non-zero exit for a device that is not plugged in")
	}
	if len(ctrl.setCalls) != 0 {
		t.Errorf("expected no state change, got %v", ctrl.setCalls)
	}
	if !strings.Contains(errOut, "no matching device is connected") {
		t.Errorf("expected an explanation, got:\n%s", errOut)
	}
}

func TestRunUSBUnknownTarget(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	_, errOut, code := runUSB(t, ctrl, true, nil, "off", "pedls")

	if code == 0 {
		t.Error("expected a non-zero exit for an unknown device")
	}
	if !strings.Contains(errOut, "no device matches") {
		t.Errorf("expected a resolution error, got:\n%s", errOut)
	}
}

func TestRunUSBMissingTarget(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	_, errOut, code := runUSB(t, ctrl, true, nil, "off")

	if code == 0 {
		t.Error("expected a non-zero exit when no device is named")
	}
	if !strings.Contains(errOut, "no device named") {
		t.Errorf("expected the omission called out, got:\n%s", errOut)
	}
}

func TestRunUSBSetEnabledFailureExitsNonZero(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices(), setErr: errors.New("access denied")}
	out, _, code := runUSB(t, ctrl, true, nil, "off", "pedals")

	if code == 0 {
		t.Error("expected a non-zero exit when the state change fails")
	}
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, "access denied") {
		t.Errorf("expected the failure reported, got:\n%s", out)
	}
}

func TestRunUSBEnumerateFailure(t *testing.T) {
	ctrl := &fakeUSBController{enumErr: errors.New("setupapi exploded")}
	_, errOut, code := runUSB(t, ctrl, true, nil, "off", "pedals")

	if code == 0 {
		t.Error("expected a non-zero exit when enumeration fails")
	}
	if !strings.Contains(errOut, "setupapi exploded") {
		t.Errorf("expected the underlying error surfaced, got:\n%s", errOut)
	}
}

func TestRunUSBRestartNoticeIsReported(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices(), restart: true}
	out, _, _ := runUSB(t, ctrl, true, nil, "off", "pedals")

	if !strings.Contains(out, "restart is needed") {
		t.Errorf("a change pending a restart must not be reported as done, got:\n%s", out)
	}
}

// TestRunUSBElevatesForStateChange covers the whole hand-off: an unelevated
// process must re-exec itself, and must replay what the elevated child printed
// rather than leaving the user with a silent command.
func TestRunUSBElevatesForStateChange(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}

	var gotArgs []string
	relaunch := func(args []string) (int, error) {
		gotArgs = args
		// Stand in for the elevated child, which reports through the file
		// named by -elevated-out because its console is not ours.
		for i, a := range args {
			if a == "-elevated-out" && i+1 < len(args) {
				os.WriteFile(args[i+1], []byte("  [+] pedals      disabled\n"), 0o644)
			}
		}
		return 0, nil
	}

	out, _, code := runUSB(t, ctrl, false, relaunch, "off", "pedals")

	if code != 0 {
		t.Errorf("exit = %d, want the child's 0", code)
	}
	if len(ctrl.setCalls) != 0 {
		t.Errorf("the unelevated parent must not touch device state, got %v", ctrl.setCalls)
	}
	if !strings.Contains(out, "[+] pedals") {
		t.Errorf("expected the child's output replayed, got:\n%s", out)
	}

	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"usb", "off", "-elevated-out", "pedals"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in the child argv, got: %v", want, gotArgs)
		}
	}
}

// TestRunUSBElevationFailureIsExplained: a refused elevation is the one failure
// the user can act on, so it must say what to do rather than just exit 1.
func TestRunUSBElevationFailureIsExplained(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	relaunch := func([]string) (int, error) { return 0, errors.New("elevation failed: cancelled") }

	_, errOut, code := runUSB(t, ctrl, false, relaunch, "off", "pedals")

	if code == 0 {
		t.Error("expected a non-zero exit when elevation fails")
	}
	if !strings.Contains(errOut, "administrator rights") {
		t.Errorf("expected guidance in the error, got:\n%s", errOut)
	}
}

// TestRunUSBElevatedChildDoesNotRecurse guards against an elevation loop: the
// child is told where to write, and must act rather than re-elevate.
func TestRunUSBElevatedChildDoesNotRecurse(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}
	path := t.TempDir() + `\child.txt`

	oldCtrl, oldElev, oldRelaunch := newUSBController, usbIsElevated, usbRelaunch
	oldOut, oldErr := usbOut, usbErrOut
	t.Cleanup(func() {
		newUSBController, usbIsElevated, usbRelaunch = oldCtrl, oldElev, oldRelaunch
		usbOut, usbErrOut = oldOut, oldErr
	})
	newUSBController = func(known []usbdev.Known) usbdev.Controller {
		ctrl.gotKnown = known
		return ctrl
	}
	usbIsElevated = func() bool { return false } // still unelevated, yet told to act
	usbRelaunch = func([]string) (int, error) {
		t.Fatal("the elevated child must never re-elevate")
		return 0, nil
	}

	code := RunUSB([]string{"off", "-elevated-out", path, "pedals"},
		filepath.Join(t.TempDir(), "launcher.config.json"))

	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if len(ctrl.setCalls) != 1 {
		t.Fatalf("expected the child to perform the change, got %v", ctrl.setCalls)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("child wrote no output file: %v", err)
	}
	if !strings.Contains(string(body), "disabled") {
		t.Errorf("expected the result in the handoff file, got: %q", body)
	}
}

// The hint must name no specific device. flag.Usage is built before the config
// is read, so listing the built-in aliases would confidently advertise four
// devices that a user with their own list does not have.
func TestUSBTargetHintNamesNoDevices(t *testing.T) {
	hint := usbTargetHint()

	for _, k := range usbdev.KnownDevices {
		if strings.Contains(hint, k.Alias) {
			t.Errorf("hint %q names the built-in device %q, which a configured rig may not have", hint, k.Alias)
		}
	}
	if !strings.Contains(hint, "all") {
		t.Errorf("expected the `all` wildcard in the usage hint, got %q", hint)
	}
}

/* ── the device list comes from the config ─────────────────────────── */

func TestRunUSBUsesTheConfiguredDeviceList(t *testing.T) {
	cfgPath := writeUSBConfig(t,
		config.USBDevice{Alias: "shifter", Name: "SIMAGIC Q1 Shifter", VID: "0x3670", PID: "0x0401"})
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}

	runUSBWithConfig(t, cfgPath, ctrl, true, nil, "list")

	if len(ctrl.gotKnown) != 1 || ctrl.gotKnown[0].Alias != "shifter" {
		t.Fatalf("controller built with %+v, want the configured device", ctrl.gotKnown)
	}
	if ctrl.gotKnown[0].VID != 0x3670 || ctrl.gotKnown[0].PID != 0x0401 {
		t.Errorf("hex IDs not parsed into the controller: %+v", ctrl.gotKnown[0])
	}
}

// A bare copy of the exe has no config, and clearing a device from one has to
// keep working — so a missing config is the built-in list, not a failure.
func TestRunUSBFallsBackToBuiltInsWithNoConfig(t *testing.T) {
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}

	out, errOut, code := runUSB(t, ctrl, true, nil, "list")

	if code != 0 {
		t.Fatalf("exit = %d, want 0 — a missing config must not fail the command", code)
	}
	// nil is what usbdev.ResolveKnown turns into the built-in rig.
	if len(ctrl.gotKnown) != 0 {
		t.Errorf("gotKnown = %+v, want nil so usbdev falls back", ctrl.gotKnown)
	}
	if !strings.Contains(out, "Built-in device list") {
		t.Errorf("listing does not disclose which list it used:\n%s", out)
	}
	// A file that simply isn't there is the normal case, not something to
	// complain about.
	if strings.Contains(errOut, "warning") {
		t.Errorf("a missing config warned: %q", errOut)
	}
}

// A malformed config warns and carries on, rather than leaving someone unable
// to re-enable their wheel because of a typo elsewhere in the file.
func TestRunUSBWarnsAndContinuesOnBadConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "launcher.config.json")
	if err := os.WriteFile(cfgPath, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}

	out, errOut, code := runUSBWithConfig(t, cfgPath, ctrl, true, nil, "list")

	if code != 0 {
		t.Fatalf("exit = %d, want 0 — a bad config must not block the command", code)
	}
	if !strings.Contains(errOut, "built-in device list") {
		t.Errorf("stderr = %q, want a warning naming the fallback", errOut)
	}
	if !strings.Contains(out, "pedals") {
		t.Errorf("the listing did not fall back to the built-ins:\n%s", out)
	}
}

func TestRunUSBListDisclosesAConfiguredList(t *testing.T) {
	cfgPath := writeUSBConfig(t,
		config.USBDevice{Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint", VID: "0x30B7", PID: "0x1001"})
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}

	out, _, _ := runUSBWithConfig(t, cfgPath, ctrl, true, nil, "list")

	// A configured list replaces the built-ins, so someone who added one device
	// has to be able to see why the rest went away.
	if !strings.Contains(out, "from usbDevices in the config") {
		t.Errorf("listing does not disclose it came from the config:\n%s", out)
	}
}

/* ── the elevated child inherits -config ───────────────────────────── */

// The child must be told which config to read. It was not, before the device
// list lived there — harmless then, but now a parent started with -config would
// resolve the target from one file while the child acted from another, silently
// toggling a different device than the one named.
func TestUSBElevatedChildInheritsConfigPath(t *testing.T) {
	cfgPath := writeUSBConfig(t,
		config.USBDevice{Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint", VID: "0x30B7", PID: "0x1001"})
	ctrl := &fakeUSBController{devs: fakeUSBDevices()}

	var childArgs []string
	relaunch := func(args []string) (int, error) {
		childArgs = args
		return 0, nil
	}

	// Unelevated, so the change has to hand off.
	runUSBWithConfig(t, cfgPath, ctrl, false, relaunch, "off", "pedals")

	joined := strings.Join(childArgs, " ")
	if !strings.Contains(joined, "-config "+cfgPath) {
		t.Fatalf("child argv = %v\nwant it to carry -config %s", childArgs, cfgPath)
	}
	// -config has to precede the subcommand, or the child's own flag parsing
	// would never see it.
	cfgAt, usbAt := indexOfArg(childArgs, "-config"), indexOfArg(childArgs, "usb")
	if cfgAt < 0 || usbAt < 0 || cfgAt > usbAt {
		t.Errorf("child argv = %v, want -config ahead of the subcommand", childArgs)
	}
	// The rest of the hand-off must survive the change.
	if !strings.Contains(joined, "-elevated-out") || !strings.HasSuffix(joined, "pedals") {
		t.Errorf("child argv = %v, want the output file and the target still in place", childArgs)
	}
}

func indexOfArg(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

/* ── scan ──────────────────────────────────────────────────────────── */

func TestRunUSBScan(t *testing.T) {
	ctrl := &fakeUSBController{
		devs: fakeUSBDevices(),
		scanned: []usbdev.Scanned{
			{VID: 0x30B7, PID: 0x1001, Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint", State: usbdev.StateEnabled, Count: 1},
			{VID: 0x046D, PID: 0xC547, Desc: "LIGHTSPEED Receiver", State: usbdev.StateEnabled, Count: 8},
		},
	}

	out, _, code := runUSB(t, ctrl, true, nil, "scan")

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"VID_30B7&PID_1001", "pedals", "VID_046D&PID_C547", "8 devices share this ID"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in the scan, got:\n%s", want, out)
		}
	}
	if len(ctrl.setCalls) != 0 {
		t.Errorf("scanning must not change any device state, got %v", ctrl.setCalls)
	}
}

// scan is what you run when the device list is wrong, so it must not be gated
// behind resolving a target against that list.
func TestRunUSBScanNeedsNoTarget(t *testing.T) {
	ctrl := &fakeUSBController{
		scanned: []usbdev.Scanned{{VID: 1, PID: 2, Desc: "Something", State: usbdev.StateEnabled, Count: 1}},
	}
	if _, _, code := runUSB(t, ctrl, true, nil, "scan"); code != 0 {
		t.Fatalf("exit = %d, want 0 with no target given", code)
	}
}

func TestRunUSBScanReportsFailure(t *testing.T) {
	ctrl := &fakeUSBController{enumErr: errors.New("setupapi exploded")}

	_, errOut, code := runUSB(t, ctrl, true, nil, "scan")

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut, "setupapi exploded") {
		t.Errorf("stderr = %q, want the underlying reason", errOut)
	}
}
