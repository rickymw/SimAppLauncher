//go:build windows

package usbdev

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	setupapi = syscall.NewLazyDLL("setupapi.dll")

	procSetupDiGetClassDevsW              = setupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiCreateDeviceInfoList       = setupapi.NewProc("SetupDiCreateDeviceInfoList")
	procSetupDiOpenDeviceInfoW            = setupapi.NewProc("SetupDiOpenDeviceInfoW")
	procSetupDiEnumDeviceInfo             = setupapi.NewProc("SetupDiEnumDeviceInfo")
	procSetupDiGetDeviceInstanceIdW       = setupapi.NewProc("SetupDiGetDeviceInstanceIdW")
	procSetupDiGetDeviceRegistryPropertyW = setupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")
	procSetupDiSetClassInstallParamsW     = setupapi.NewProc("SetupDiSetClassInstallParamsW")
	procSetupDiCallClassInstaller         = setupapi.NewProc("SetupDiCallClassInstaller")
	procSetupDiGetDeviceInstallParamsW    = setupapi.NewProc("SetupDiGetDeviceInstallParamsW")
	procSetupDiDestroyDeviceInfoList      = setupapi.NewProc("SetupDiDestroyDeviceInfoList")

	cfgmgr32               = syscall.NewLazyDLL("cfgmgr32.dll")
	procCMGetDevNodeStatus = cfgmgr32.NewProc("CM_Get_DevNode_Status")
)

const (
	digcfPresent    = 0x02
	digcfAllClasses = 0x04

	spdrpDeviceDesc = 0x00
	spdrpService    = 0x04

	difPropertyChange = 0x12

	dicsEnable  = 1
	dicsDisable = 2

	dicsFlagGlobal         = 1
	dicsFlagConfigSpecific = 2

	// cmProbDisabled is the problem code a devnode carries when it has been
	// disabled by hand — the state this package sets and clears.
	cmProbDisabled = 22

	dnHasProblem = 0x00000400

	diNeedRestart = 0x00000080
	diNeedReboot  = 0x00000100

	errorNoMoreItems  = 259
	errorAccessDenied = 5

	invalidHandleValue = ^uintptr(0)
)

type spDevInfoData struct {
	cbSize    uint32
	classGUID [16]byte
	devInst   uint32
	reserved  uintptr
}

type spClassInstallHeader struct {
	cbSize          uint32
	installFunction uint32
}

type spPropChangeParams struct {
	header      spClassInstallHeader
	stateChange uint32
	scope       uint32
	hwProfile   uint32
}

type spDevInstallParams struct {
	cbSize                   uint32
	flags                    uint32
	flagsEx                  uint32
	hwndParent               uintptr
	installMsgHandler        uintptr
	installMsgHandlerContext uintptr
	fileQueue                uintptr
	classInstallReserved     uintptr
	reserved                 uint32
	driverPath               [260]uint16
}

type winController struct {
	known []Known
}

// NewController returns the Windows implementation of Controller, matching
// devices against known.
//
// The list is passed in rather than read from the package global because it now
// comes from the user's config. Pass ResolveKnown(nil) for the built-in rig.
func NewController(known []Known) Controller {
	return &winController{known: ResolveKnown(known)}
}

// devInfoSet opens a device information set. enumerator may be "USB" for every
// USB device, or a full device instance ID to get a set holding just that one.
func devInfoSet(enumerator string) (uintptr, error) {
	enum, err := syscall.UTF16PtrFromString(enumerator)
	if err != nil {
		return 0, err
	}
	h, _, callErr := procSetupDiGetClassDevsW.Call(
		0, // ClassGuid — NULL, required when DIGCF_ALLCLASSES is set
		uintptr(unsafe.Pointer(enum)),
		0,
		uintptr(digcfPresent|digcfAllClasses),
	)
	if h == invalidHandleValue {
		return 0, fmt.Errorf("SetupDiGetClassDevs(%s): %w", enumerator, callErr)
	}
	return h, nil
}

func destroyDevInfoSet(h uintptr) {
	procSetupDiDestroyDeviceInfoList.Call(h)
}

// openDevice returns a device information set holding exactly the one device
// named by instanceID.
//
// SetupDiGetClassDevs documents its Enumerator parameter as accepting a device
// instance ID, but rejects one with ERROR_INVALID_DATA in practice (verified
// against this rig). Building an empty set and opening the single device into
// it is the unambiguous API for "this device and nothing else", and matters
// here because the alternative — enumerating and filtering — would leave the
// door open to acting on a sibling node.
func openDevice(instanceID string) (uintptr, spDevInfoData, error) {
	var did spDevInfoData

	h, _, callErr := procSetupDiCreateDeviceInfoList.Call(0, 0)
	if h == invalidHandleValue {
		return 0, did, fmt.Errorf("SetupDiCreateDeviceInfoList: %w", callErr)
	}

	id, err := syscall.UTF16PtrFromString(instanceID)
	if err != nil {
		destroyDevInfoSet(h)
		return 0, did, err
	}

	did.cbSize = uint32(unsafe.Sizeof(did))
	ret, _, callErr := procSetupDiOpenDeviceInfoW.Call(
		h,
		uintptr(unsafe.Pointer(id)),
		0,
		0,
		uintptr(unsafe.Pointer(&did)),
	)
	if ret == 0 {
		destroyDevInfoSet(h)
		return 0, did, fmt.Errorf("device %s: %w", instanceID, callErr)
	}
	return h, did, nil
}

// enumDevice fills did with the device at index, reporting done at the end of
// the set.
func enumDevice(h uintptr, index int, did *spDevInfoData) (done bool, err error) {
	did.cbSize = uint32(unsafe.Sizeof(*did))
	ret, _, callErr := procSetupDiEnumDeviceInfo.Call(h, uintptr(index), uintptr(unsafe.Pointer(did)))
	if ret == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == errorNoMoreItems {
			return true, nil
		}
		return false, fmt.Errorf("SetupDiEnumDeviceInfo: %w", callErr)
	}
	return false, nil
}

func deviceInstanceID(h uintptr, did *spDevInfoData) (string, error) {
	buf := make([]uint16, 512)
	ret, _, callErr := procSetupDiGetDeviceInstanceIdW.Call(
		h,
		uintptr(unsafe.Pointer(did)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)), // in characters
		0,
	)
	if ret == 0 {
		return "", fmt.Errorf("SetupDiGetDeviceInstanceId: %w", callErr)
	}
	return syscall.UTF16ToString(buf), nil
}

// deviceDesc reads the device description. A failure is not an error: the
// description is diagnostic garnish next to the curated name in KnownDevices,
// and a device we can otherwise identify and toggle should not be dropped from
// the listing because one cosmetic property could not be read.
func deviceDesc(h uintptr, did *spDevInfoData) string {
	return deviceProperty(h, did, spdrpDeviceDesc)
}

// deviceService reads the driver service backing a device, which is how a hub
// is told from a peripheral.
//
// The service is used rather than the description because the description is
// localised — "Generic USB Hub" is only that string on an English Windows,
// while the service name is a driver identifier and is not translated.
func deviceService(h uintptr, did *spDevInfoData) string {
	return deviceProperty(h, did, spdrpService)
}

// isHubService reports whether a device is backed by a hub or host-controller
// driver. The name test lives in the cross-platform file so it can be tested
// without a rig.
func isHubService(h uintptr, did *spDevInfoData) bool {
	return IsHubServiceName(deviceService(h, did))
}

func deviceProperty(h uintptr, did *spDevInfoData, prop uint32) string {
	buf := make([]uint16, 512)
	ret, _, _ := procSetupDiGetDeviceRegistryPropertyW.Call(
		h,
		uintptr(unsafe.Pointer(did)),
		uintptr(prop),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)*2), // in bytes
		0,
	)
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// devNodeState reports whether a present devnode is enabled or disabled.
func devNodeState(devInst uint32) State {
	var status, problem uint32
	ret, _, _ := procCMGetDevNodeStatus.Call(
		uintptr(unsafe.Pointer(&status)),
		uintptr(unsafe.Pointer(&problem)),
		uintptr(devInst),
		0,
	)
	if ret != 0 { // CR_SUCCESS == 0
		return StateEnabled
	}
	if status&dnHasProblem != 0 && problem == cmProbDisabled {
		return StateDisabled
	}
	return StateEnabled
}

// walkUSB visits every top-level USB device node on the machine.
//
// Both Enumerate and Scan are built on it: they used to be the same walk with
// and without a discard, and keeping one traversal means a device the picker
// offers is by construction a device the toggler can find.
func walkUSB(visit func(h uintptr, instanceID string, did *spDevInfoData)) error {
	h, err := devInfoSet("USB")
	if err != nil {
		return err
	}
	defer destroyDevInfoSet(h)

	for i := 0; ; i++ {
		var did spDevInfoData
		done, err := enumDevice(h, i, &did)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		instanceID, err := deviceInstanceID(h, &did)
		if err != nil {
			continue
		}
		visit(h, instanceID, &did)
	}
}

func (c *winController) Enumerate() ([]Device, error) {
	// Start from the known list so a device that is not plugged in is still
	// reported, as StateAbsent, rather than silently missing from the table.
	devs := make([]Device, 0, len(c.known))
	for _, k := range c.known {
		devs = append(devs, Device{Known: k, State: StateAbsent})
	}
	byAlias := make(map[string]*Device, len(devs))
	for i := range devs {
		byAlias[devs[i].Alias] = &devs[i]
	}

	// The device info set handle is needed to read a description, and walkUSB
	// owns it — so the description is read inside the callback rather than
	// after the walk.
	err := walkUSB(func(h uintptr, instanceID string, did *spDevInfoData) {
		known, ok := match(c.known, instanceID)
		if !ok {
			return
		}
		d := byAlias[known.Alias]
		d.InstanceID = instanceID
		d.Desc = deviceDesc(h, did)
		d.State = devNodeState(did.devInst)
	})
	if err != nil {
		return nil, err
	}

	SortDevices(devs)
	return devs, nil
}

func (c *winController) Scan() ([]Scanned, error) {
	// Grouped by hardware ID, because that is what a device list entry actually
	// selects. This rig reports eight identical LIGHTSPEED receivers and seven
	// generic hubs sharing one VID/PID; listing each devnode would offer the
	// same "device" eight times, and adding any one of them would match all
	// eight. Count carries that fact rather than hiding it.
	byID := make(map[[2]uint16]*Scanned)
	var order [][2]uint16

	err := walkUSB(func(h uintptr, instanceID string, did *spDevInfoData) {
		// parseVIDPID rejects interface nodes (&MI_), which is what keeps a
		// composite device like the MOZA handbrake from appearing once per USB
		// interface when only the top-level node is toggleable.
		vid, pid, ok := parseVIDPID(instanceID)
		if !ok {
			return
		}

		// Hubs are dropped. They are the majority of what a USB walk returns —
		// 27 of 63 nodes here — none of them is a sim control, and disabling
		// one would take down everything plugged into it. Excluding them is
		// what makes the picker a list someone can read.
		if isHubService(h, did) {
			return
		}

		key := [2]uint16{vid, pid}
		if existing, seen := byID[key]; seen {
			existing.Count++
			// A group is disabled only if every node in it is: `usb off` on
			// this hardware ID would have to disable them all to count.
			if existing.State != StateDisabled {
				existing.State = StateEnabled
			}
			return
		}

		s := &Scanned{
			InstanceID: instanceID,
			Desc:       deviceDesc(h, did),
			VID:        vid,
			PID:        pid,
			State:      devNodeState(did.devInst),
			Count:      1,
		}
		if known, isKnown := match(c.known, instanceID); isKnown {
			s.Alias, s.Name = known.Alias, known.Name
		}
		byID[key] = s
		order = append(order, key)
	})
	if err != nil {
		return nil, err
	}

	found := make([]Scanned, 0, len(order))
	for _, key := range order {
		found = append(found, *byID[key])
	}
	SortScanned(found)
	return found, nil
}

func (c *winController) SetEnabled(instanceID string, enable bool) (bool, error) {
	h, did, err := openDevice(instanceID)
	if err != nil {
		return false, err
	}
	defer destroyDevInfoSet(h)

	// Disable is global. Enable applies the config-specific scope first and the
	// global one second: a device can be disabled in the current hardware
	// profile, globally, or both, and clearing only one scope leaves it
	// disabled while reporting success.
	scopes := []uint32{dicsFlagGlobal}
	state := uint32(dicsDisable)
	if enable {
		scopes = []uint32{dicsFlagConfigSpecific, dicsFlagGlobal}
		state = dicsEnable
	}

	for _, scope := range scopes {
		if err := propertyChange(h, &did, state, scope); err != nil {
			return false, err
		}
	}
	return restartPending(h, &did), nil
}

func propertyChange(h uintptr, did *spDevInfoData, stateChange, scope uint32) error {
	params := spPropChangeParams{
		header:      spClassInstallHeader{installFunction: difPropertyChange},
		stateChange: stateChange,
		scope:       scope,
	}
	params.header.cbSize = uint32(unsafe.Sizeof(params.header))

	ret, _, callErr := procSetupDiSetClassInstallParamsW.Call(
		h,
		uintptr(unsafe.Pointer(did)),
		uintptr(unsafe.Pointer(&params)),
		uintptr(unsafe.Sizeof(params)),
	)
	if ret == 0 {
		return fmt.Errorf("SetupDiSetClassInstallParams: %w", callErr)
	}

	ret, _, callErr = procSetupDiCallClassInstaller.Call(
		uintptr(difPropertyChange),
		h,
		uintptr(unsafe.Pointer(did)),
	)
	if ret == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == errorAccessDenied {
			return fmt.Errorf("access denied — changing device state requires an elevated process")
		}
		return fmt.Errorf("SetupDiCallClassInstaller: %w", callErr)
	}
	return nil
}

// restartPending reports whether Windows wants a restart before the change
// takes effect. It is effectively never true for a USB HID device, but
// reporting success for a toggle that has not actually happened yet would send
// the user hunting for a fault in the game instead.
func restartPending(h uintptr, did *spDevInfoData) bool {
	var params spDevInstallParams
	params.cbSize = uint32(unsafe.Sizeof(params))
	ret, _, _ := procSetupDiGetDeviceInstallParamsW.Call(
		h,
		uintptr(unsafe.Pointer(did)),
		uintptr(unsafe.Pointer(&params)),
	)
	if ret == 0 {
		return false
	}
	return params.flags&(diNeedRestart|diNeedReboot) != 0
}
