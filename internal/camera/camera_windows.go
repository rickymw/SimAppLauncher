//go:build windows

package camera

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

var (
	advapi32Camera         = syscall.NewLazyDLL("advapi32.dll")
	procOpenSCManagerW     = advapi32Camera.NewProc("OpenSCManagerW")
	procOpenServiceW       = advapi32Camera.NewProc("OpenServiceW")
	procControlService     = advapi32Camera.NewProc("ControlService")
	procQueryServiceStatus = advapi32Camera.NewProc("QueryServiceStatus")
	procStartServiceW      = advapi32Camera.NewProc("StartServiceW")
	procCloseServiceHandle = advapi32Camera.NewProc("CloseServiceHandle")
)

const (
	scManagerConnect   = 0x0001
	serviceQueryStatus = 0x0004
	serviceStart       = 0x0010
	serviceStop        = 0x0020

	serviceControlStop = 1

	serviceStopped = 1
	serviceRunning = 4
)

type winServiceStatus struct {
	ServiceType             uint32
	CurrentState            uint32
	ControlsAccepted        uint32
	Win32ExitCode           uint32
	ServiceSpecificExitCode uint32
	CheckPoint              uint32
	WaitHint                uint32
}

// frameServerServices are the Windows services that mediate camera access.
// Stopping and restarting them clears a stuck/frozen camera without touching
// the USB PnP device itself, which requires an administrator token. These
// two do not require full admin rights — just SERVICE_START/SERVICE_STOP on
// the services themselves, which can be granted to a specific account via a
// one-time `sc sdset` change (see README.md).
var frameServerServices = []string{"FrameServer", "FrameServerMonitor"}

type serviceRestarter struct{}

// NewRestarter returns the Windows implementation of Restarter.
func NewRestarter() Restarter {
	return &serviceRestarter{}
}

func (s *serviceRestarter) Restart(progress func(string)) ([]ServiceResult, error) {
	var results []ServiceResult
	for _, name := range frameServerServices {
		restarted, err := restartService(name, progress)
		if err != nil {
			return results, fmt.Errorf("%s: %w", name, err)
		}
		results = append(results, ServiceResult{Name: name, Restarted: restarted})
	}
	return results, nil
}

// restartService stops and restarts name, returning whether it did anything.
// A service that is already stopped is left alone: both services are
// DEMAND_START, so Windows starts them when an app next opens the camera, and
// starting them here would leave services running that were meant to be idle.
// There is also no stuck pipeline state to clear when nothing is running.
func restartService(name string, progress func(string)) (bool, error) {
	scm, err := openSCManager()
	if err != nil {
		return false, err
	}
	defer closeServiceHandle(scm)

	svc, err := openService(scm, name, serviceQueryStatus|serviceStart|serviceStop)
	if err != nil {
		return false, err
	}
	defer closeServiceHandle(svc)

	status, err := queryServiceStatus(svc)
	if err != nil {
		return false, err
	}
	if status.CurrentState == serviceStopped {
		return false, nil
	}

	if err := controlServiceStop(svc); err != nil {
		return false, err
	}
	slowNotice := func() {
		if progress != nil {
			progress(fmt.Sprintf("      %s is still in use — waiting for the app holding the camera to release it (can take ~30s)", name))
		}
	}
	if err := waitForState(svc, serviceStopped, stopTimeout, slowNotice, slowStopNotice); err != nil {
		return false, err
	}

	if err := startService(svc); err != nil {
		return false, err
	}
	if err := waitForState(svc, serviceRunning, startTimeout, nil, 0); err != nil {
		return false, err
	}
	return true, nil
}

func openSCManager() (syscall.Handle, error) {
	r, _, err := procOpenSCManagerW.Call(0, 0, scManagerConnect)
	if r == 0 {
		return 0, fmt.Errorf("OpenSCManagerW: %w", err)
	}
	return syscall.Handle(r), nil
}

func openService(scm syscall.Handle, name string, access uint32) (syscall.Handle, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	r, _, callErr := procOpenServiceW.Call(uintptr(scm), uintptr(unsafe.Pointer(namePtr)), uintptr(access))
	if r == 0 {
		return 0, fmt.Errorf("OpenServiceW(%s): %w", name, callErr)
	}
	return syscall.Handle(r), nil
}

func queryServiceStatus(svc syscall.Handle) (winServiceStatus, error) {
	var status winServiceStatus
	r, _, err := procQueryServiceStatus.Call(uintptr(svc), uintptr(unsafe.Pointer(&status)))
	if r == 0 {
		return status, fmt.Errorf("QueryServiceStatus: %w", err)
	}
	return status, nil
}

func controlServiceStop(svc syscall.Handle) error {
	var status winServiceStatus
	r, _, err := procControlService.Call(uintptr(svc), serviceControlStop, uintptr(unsafe.Pointer(&status)))
	if r == 0 {
		return fmt.Errorf("ControlService(stop): %w", err)
	}
	return nil
}

func startService(svc syscall.Handle) error {
	r, _, err := procStartServiceW.Call(uintptr(svc), 0, 0)
	if r == 0 {
		return fmt.Errorf("StartServiceW: %w", err)
	}
	return nil
}

const (
	// Stop/start measure at 20–110ms each when the camera is idle, so the poll
	// interval is kept well below that — at 200ms the four waits rounded up to
	// roughly half a second of dead time, most of the command's runtime.
	pollInterval = 15 * time.Millisecond

	// A stop measured at 30.8s with the camera actively streaming: Windows waits
	// for the holding client to release the device before the service will stop.
	// The timeout must clear that comfortably, otherwise a restart that is merely
	// slow gets reported as a failure. (It was 10s, which did exactly that.)
	stopTimeout  = 90 * time.Second
	startTimeout = 15 * time.Second

	// How long a stop may take before telling the user why it is waiting, rather
	// than leaving them looking at an apparently hung window.
	slowStopNotice = 2 * time.Second
)

// waitForState polls until the service reaches want. If it has not done so
// within notifyAfter, notify is called once so slow waits can be explained.
func waitForState(svc syscall.Handle, want uint32, timeout time.Duration, notify func(), notifyAfter time.Duration) error {
	start := time.Now()
	deadline := start.Add(timeout)
	notified := false
	for {
		status, err := queryServiceStatus(svc)
		if err != nil {
			return err
		}
		if status.CurrentState == want {
			return nil
		}
		if !notified && notify != nil && time.Since(start) >= notifyAfter {
			notify()
			notified = true
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for state %d", timeout, want)
		}
		time.Sleep(pollInterval)
	}
}

func closeServiceHandle(h syscall.Handle) {
	procCloseServiceHandle.Call(uintptr(h))
}
