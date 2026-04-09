//go:build windows

// Package rawinput detects HID joystick/gamepad button presses via the Windows
// Raw Input API. Used by the notes subcommand to support steering wheel buttons
// without requiring a keyboard mapping in iRacing.
package rawinput

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// HIDButton identifies a specific button on a specific HID device.
type HIDButton struct {
	VendorID  uint16 // USB VID
	ProductID uint16 // USB PID
	Button    uint16 // 1-based button number as reported by HID
}

// String formats b as "HID:VVVV:PPPP:N" — the form stored in launcher.config.json.
func (b HIDButton) String() string {
	return fmt.Sprintf("HID:%04X:%04X:%d", b.VendorID, b.ProductID, b.Button)
}

// IsHIDHotkey reports whether s is a HID hotkey string (prefix "HID:").
func IsHIDHotkey(s string) bool {
	return strings.HasPrefix(strings.ToUpper(s), "HID:")
}

// ParseHIDButton parses a "HID:VVVV:PPPP:N" string back to a HIDButton.
// Returns the zero value and false if s is not a valid HID hotkey string.
func ParseHIDButton(s string) (HIDButton, bool) {
	if !IsHIDHotkey(s) {
		return HIDButton{}, false
	}
	parts := strings.Split(s[4:], ":") // after "HID:"
	if len(parts) != 3 {
		return HIDButton{}, false
	}
	vid, e1 := strconv.ParseUint(parts[0], 16, 16)
	pid, e2 := strconv.ParseUint(parts[1], 16, 16)
	btn, e3 := strconv.ParseUint(parts[2], 10, 16)
	if e1 != nil || e2 != nil || e3 != nil {
		return HIDButton{}, false
	}
	return HIDButton{uint16(vid), uint16(pid), uint16(btn)}, true
}

// State tracks per-device button states for press/release detection
// and caches preparsed HID data to avoid redundant kernel calls.
type State struct {
	prevPressed    map[uintptr]map[uint16]bool // hDevice → set of pressed button usages
	preparsedCache map[uintptr][]byte          // hDevice → preparsed data
}

// NewState returns an initialised State ready for use.
func NewState() *State {
	return &State{
		prevPressed:    make(map[uintptr]map[uint16]bool),
		preparsedCache: make(map[uintptr][]byte),
	}
}

// ---- Windows API constants ----

const (
	ridevInputSink   = 0x00000100 // receive input even when not in foreground
	rimTypeHID       = 2          // raw input from a generic HID device
	ridInput         = 0x10000003 // GetRawInputData: get the full input packet
	ridiDeviceName   = 0x20000007 // GetRawInputDeviceInfoW: get device name string
	ridiPreparsedData = 0x20000005 // GetRawInputDeviceInfoW: get preparsed HID data

	hidUsagePageGenericDesktop = 0x01
	hidUsageJoystick           = 0x04
	hidUsageGamePad            = 0x05
	hidUsageMultiAxisController = 0x08
	hidUsagePageButton         = 0x09

	hidpInput         = 0          // HidP_Input report type
	hidpStatusSuccess = 0x00110000 // HIDP_STATUS_SUCCESS

	// RAWINPUTHEADER size on 64-bit: dwType(4)+dwSize(4)+hDevice(8)+wParam(8) = 24
	rawInputHeaderSize = 24
)

// ---- Windows struct mirrors ----

// rawInputDevice mirrors RAWINPUTDEVICE (16 bytes on 64-bit).
type rawInputDevice struct {
	UsUsagePage uint16
	UsUsage     uint16
	DwFlags     uint32
	HwndTarget  uintptr
}

// ---- DLL procs ----

var (
	modUser32 = syscall.NewLazyDLL("user32.dll")
	modHID    = syscall.NewLazyDLL("hid.dll")

	procRegisterRawInputDevices = modUser32.NewProc("RegisterRawInputDevices")
	procGetRawInputData         = modUser32.NewProc("GetRawInputData")
	procGetRawInputDeviceInfo   = modUser32.NewProc("GetRawInputDeviceInfoW")
	procCreateWindowExW         = modUser32.NewProc("CreateWindowExW")
	procHidPGetUsages           = modHID.NewProc("HidP_GetUsages")
)

// rawInputHwnd is the hidden window we create as the WM_INPUT target.
// It must belong to this process so GetMessageW delivers messages to our loop.
// Stored at package level; valid for the lifetime of the process.
var rawInputHwnd uintptr

// createHiddenWindow creates a minimal invisible WS_POPUP window owned by this
// process and thread. Must be called after runtime.LockOSThread() so that the
// window lives on the same thread as the GetMessageW loop.
func createHiddenWindow() (uintptr, error) {
	// "STATIC" is a pre-registered Windows class — no RegisterClassEx needed.
	cls, err := syscall.UTF16PtrFromString("STATIC")
	if err != nil {
		return 0, err
	}
	// WS_EX_TOOLWINDOW hides the window from the taskbar and Alt-Tab.
	// WS_POPUP creates a borderless window with no taskbar button.
	// Not passing WS_VISIBLE keeps it off-screen.
	const wsExToolWindow = 0x00000080
	const wsPopup = 0x80000000
	hwnd, _, lastErr := procCreateWindowExW.Call(
		wsExToolWindow,                      // dwExStyle
		uintptr(unsafe.Pointer(cls)),        // lpClassName
		0,                                   // lpWindowName (NULL)
		wsPopup,                             // dwStyle
		0, 0, 0, 0,                          // x, y, width, height
		0,                                   // hWndParent (NULL)
		0,                                   // hMenu
		0,                                   // hInstance
		0,                                   // lpParam
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("CreateWindowExW: %w", lastErr)
	}
	return hwnd, nil
}

// Register subscribes to HID joystick/gamepad/multi-axis raw input.
// Must be called after runtime.LockOSThread() and before the GetMessageW loop
// so the hidden target window lives on the message-loop thread.
// RIDEV_INPUTSINK ensures WM_INPUT arrives even while iRacing is in the foreground.
func Register() error {
	if rawInputHwnd == 0 {
		hwnd, err := createHiddenWindow()
		if err != nil {
			return fmt.Errorf("raw input window: %w", err)
		}
		rawInputHwnd = hwnd
	}

	devices := [3]rawInputDevice{
		{UsUsagePage: hidUsagePageGenericDesktop, UsUsage: hidUsageJoystick, DwFlags: ridevInputSink, HwndTarget: rawInputHwnd},
		{UsUsagePage: hidUsagePageGenericDesktop, UsUsage: hidUsageGamePad, DwFlags: ridevInputSink, HwndTarget: rawInputHwnd},
		{UsUsagePage: hidUsagePageGenericDesktop, UsUsage: hidUsageMultiAxisController, DwFlags: ridevInputSink, HwndTarget: rawInputHwnd},
	}
	ret, _, lastErr := procRegisterRawInputDevices.Call(
		uintptr(unsafe.Pointer(&devices[0])),
		uintptr(len(devices)),
		unsafe.Sizeof(devices[0]),
	)
	if ret == 0 {
		return fmt.Errorf("RegisterRawInputDevices: %w", lastErr)
	}
	return nil
}

// CaptureAnyButton parses a WM_INPUT lParam and returns the first newly-pressed
// button on any HID device. Used by set-hotkey to let the user press any button.
func CaptureAnyButton(lParam uintptr, state *State) (HIDButton, bool) {
	hDevice, reportData, ok := parseRawHID(lParam)
	if !ok {
		return HIDButton{}, false
	}

	vid, pid := vidpidFromDevice(hDevice)
	current := pressedButtons(hDevice, reportData, state)

	prev := state.prevPressed[hDevice]
	state.prevPressed[hDevice] = current

	for btn := range current {
		if !prev[btn] {
			return HIDButton{VendorID: vid, ProductID: pid, Button: btn}, true
		}
	}
	return HIDButton{}, false
}

// HandleButtonEvent parses a WM_INPUT lParam and, if the event matches target,
// sends to pressedCh on button-down or releasedCh on button-up.
func HandleButtonEvent(lParam uintptr, target HIDButton, state *State, pressedCh, releasedCh chan<- struct{}) {
	hDevice, reportData, ok := parseRawHID(lParam)
	if !ok {
		return
	}

	vid, pid := vidpidFromDevice(hDevice)
	if vid != target.VendorID || pid != target.ProductID {
		return
	}

	current := pressedButtons(hDevice, reportData, state)
	prev := state.prevPressed[hDevice]
	state.prevPressed[hDevice] = current

	wasPressed := prev[target.Button]
	isPressed := current[target.Button]

	if isPressed && !wasPressed {
		select {
		case pressedCh <- struct{}{}:
		default:
		}
	}
	if !isPressed && wasPressed {
		select {
		case releasedCh <- struct{}{}:
		default:
		}
	}
}

// ---- Internal helpers ----

// parseRawHID reads the raw input buffer from a WM_INPUT lParam.
// Returns the device handle, the first HID report's bytes, and whether it succeeded.
func parseRawHID(lParam uintptr) (hDevice uintptr, reportData []byte, ok bool) {
	var dataSize uint32
	procGetRawInputData.Call(lParam, ridInput, 0, uintptr(unsafe.Pointer(&dataSize)), rawInputHeaderSize)
	if dataSize == 0 {
		return 0, nil, false
	}

	buf := make([]byte, dataSize)
	ret, _, _ := procGetRawInputData.Call(lParam, ridInput, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&dataSize)), rawInputHeaderSize)
	if ret == ^uintptr(0) {
		return 0, nil, false
	}

	// RAWINPUTHEADER: dwType(0:4), dwSize(4:8), hDevice(8:16), wParam(16:24)
	if len(buf) < rawInputHeaderSize+8 {
		return 0, nil, false
	}
	dwType := *(*uint32)(unsafe.Pointer(&buf[0]))
	if dwType != rimTypeHID {
		return 0, nil, false
	}
	hDevice = *(*uintptr)(unsafe.Pointer(&buf[8]))

	// RAWHID starts at offset 24: dwSizeHid(4) + dwCount(4) + bRawData
	dwSizeHid := *(*uint32)(unsafe.Pointer(&buf[rawInputHeaderSize]))
	dwCount := *(*uint32)(unsafe.Pointer(&buf[rawInputHeaderSize+4]))
	if dwCount == 0 || dwSizeHid == 0 {
		return 0, nil, false
	}
	dataStart := rawInputHeaderSize + 8
	if len(buf) < dataStart+int(dwSizeHid) {
		return 0, nil, false
	}
	return hDevice, buf[dataStart : dataStart+int(dwSizeHid)], true
}

// vidpidFromDevice extracts USB Vendor ID and Product ID from the device's
// instance path string (e.g. "\\?\HID#VID_0483&PID_A355&...").
func vidpidFromDevice(hDevice uintptr) (vid, pid uint16) {
	var nameSize uint32
	procGetRawInputDeviceInfo.Call(hDevice, ridiDeviceName, 0, uintptr(unsafe.Pointer(&nameSize)))
	if nameSize == 0 {
		return 0, 0
	}
	nameBuf := make([]uint16, nameSize)
	procGetRawInputDeviceInfo.Call(hDevice, ridiDeviceName, uintptr(unsafe.Pointer(&nameBuf[0])), uintptr(unsafe.Pointer(&nameSize)))
	name := strings.ToUpper(syscall.UTF16ToString(nameBuf))

	if i := strings.Index(name, "VID_"); i >= 0 && len(name) >= i+8 {
		n, _ := strconv.ParseUint(name[i+4:i+8], 16, 16)
		vid = uint16(n)
	}
	if i := strings.Index(name, "PID_"); i >= 0 && len(name) >= i+8 {
		n, _ := strconv.ParseUint(name[i+4:i+8], 16, 16)
		pid = uint16(n)
	}
	return vid, pid
}

// pressedButtons returns the set of currently-pressed button usages for a device.
// Preparsed HID data is cached per device handle to avoid repeated kernel calls.
func pressedButtons(hDevice uintptr, reportData []byte, state *State) map[uint16]bool {
	preparsed := state.preparsedCache[hDevice]
	if preparsed == nil {
		var size uint32
		procGetRawInputDeviceInfo.Call(hDevice, ridiPreparsedData, 0, uintptr(unsafe.Pointer(&size)))
		if size == 0 {
			return nil
		}
		preparsed = make([]byte, size)
		procGetRawInputDeviceInfo.Call(hDevice, ridiPreparsedData, uintptr(unsafe.Pointer(&preparsed[0])), uintptr(unsafe.Pointer(&size)))
		state.preparsedCache[hDevice] = preparsed
	}

	// HidP_GetUsages returns the set of currently-pressed button usages.
	// We allocate generously; the call updates numUsages with the actual count.
	var numUsages uint32 = 256
	usages := make([]uint16, numUsages)
	status, _, _ := procHidPGetUsages.Call(
		uintptr(hidpInput),
		uintptr(hidUsagePageButton),
		uintptr(0), // LinkCollection = 0 (all)
		uintptr(unsafe.Pointer(&usages[0])),
		uintptr(unsafe.Pointer(&numUsages)),
		uintptr(unsafe.Pointer(&preparsed[0])),
		uintptr(unsafe.Pointer(&reportData[0])),
		uintptr(len(reportData)),
	)
	if uint32(status) != hidpStatusSuccess {
		return nil
	}

	result := make(map[uint16]bool, numUsages)
	for _, u := range usages[:numUsages] {
		if u != 0 {
			result[u] = true
		}
	}
	return result
}
