package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rickymw/MotorHome/internal/usbdev"
)

type fakeUSBController struct {
	devs     []usbdev.Device
	enumErr  error
	setErr   error
	restart  bool
	setCalls []fakeSetCall
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
func runUSB(t *testing.T, ctrl usbdev.Controller, elevated bool, relaunch func([]string) (int, error), args ...string) (string, string, int) {
	t.Helper()

	oldOut, oldErr := usbOut, usbErrOut
	oldCtrl, oldElev, oldRelaunch := newUSBController, usbIsElevated, usbRelaunch
	t.Cleanup(func() {
		usbOut, usbErrOut = oldOut, oldErr
		newUSBController, usbIsElevated, usbRelaunch = oldCtrl, oldElev, oldRelaunch
	})

	var out, errBuf bytes.Buffer
	usbOut, usbErrOut = &out, &errBuf
	newUSBController = func() usbdev.Controller { return ctrl }
	usbIsElevated = func() bool { return elevated }
	usbRelaunch = relaunch
	if relaunch == nil {
		usbRelaunch = func([]string) (int, error) {
			t.Fatal("did not expect an elevation attempt")
			return 0, nil
		}
	}

	code := RunUSB(args)
	return out.String(), errBuf.String(), code
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
	newUSBController = func() usbdev.Controller { return ctrl }
	usbIsElevated = func() bool { return false } // still unelevated, yet told to act
	usbRelaunch = func([]string) (int, error) {
		t.Fatal("the elevated child must never re-elevate")
		return 0, nil
	}

	code := RunUSB([]string{"off", "-elevated-out", path, "pedals"})

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

func TestUSBTargetHint(t *testing.T) {
	hint := usbTargetHint()
	for _, k := range usbdev.KnownDevices {
		if !strings.Contains(hint, k.Alias) {
			t.Errorf("expected %q in the usage hint, got %q", k.Alias, hint)
		}
	}
	if !strings.Contains(hint, "all") {
		t.Errorf("expected `all` in the usage hint, got %q", hint)
	}
}
