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

type winController struct{}

// NewController returns the Windows implementation of Controller.
func NewController() Controller { return &winController{} }

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
	buf := make([]uint16, 512)
	ret, _, _ := procSetupDiGetDeviceRegistryPropertyW.Call(
		h,
		uintptr(unsafe.Pointer(did)),
		uintptr(spdrpDeviceDesc),
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

func (c *winController) Enumerate() ([]Device, error) {
	// Start from the known list so a device that is not plugged in is still
	// reported, as StateAbsent, rather than silently missing from the table.
	devs := make([]Device, 0, len(KnownDevices))
	for _, k := range KnownDevices {
		devs = append(devs, Device{Known: k, State: StateAbsent})
	}
	byAlias := make(map[string]*Device, len(devs))
	for i := range devs {
		byAlias[devs[i].Alias] = &devs[i]
	}

	h, err := devInfoSet("USB")
	if err != nil {
		return nil, err
	}
	defer destroyDevInfoSet(h)

	for i := 0; ; i++ {
		var did spDevInfoData
		done, err := enumDevice(h, i, &did)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}

		instanceID, err := deviceInstanceID(h, &did)
		if err != nil {
			continue
		}
		known, ok := match(instanceID)
		if !ok {
			continue
		}

		d := byAlias[known.Alias]
		d.InstanceID = instanceID
		d.Desc = deviceDesc(h, &did)
		d.State = devNodeState(did.devInst)
	}

	SortDevices(devs)
	return devs, nil
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
