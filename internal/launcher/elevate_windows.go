//go:build windows

package launcher

import (
	"strings"
	"syscall"
	"unsafe"

	"github.com/rickymw/MotorHome/internal/config"
)

// shellExecuteInfo maps to SHELLEXECUTEINFOW (64-bit layout).
type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      uintptr
	dwHotKey       uint32
	_              uint32 // padding / hIcon or hMonitor union
	hProcess       uintptr
}

const seeMaskNoCloseProcess = 0x00000040

var (
	shell32        = syscall.NewLazyDLL("shell32.dll")
	shellExecuteEx = shell32.NewProc("ShellExecuteExW")
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	getProcessId   = kernel32.NewProc("GetProcessId")
)

func spawnElevated(app config.App) SpawnResult {
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, err := syscall.UTF16PtrFromString(app.Path)
	if err != nil {
		return SpawnResult{Err: err}
	}

	var params *uint16
	if app.Args != "" {
		params, err = syscall.UTF16PtrFromString(strings.TrimSpace(app.Args))
		if err != nil {
			return SpawnResult{Err: err}
		}
	}

	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: params,
		nShow:        1, // SW_SHOWNORMAL
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	r, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return SpawnResult{Err: callErr}
	}

	if info.hProcess == 0 {
		return SpawnResult{PID: 0}
	}

	pid, _, _ := getProcessId.Call(info.hProcess)
	syscall.CloseHandle(syscall.Handle(info.hProcess))
	return SpawnResult{PID: int(pid)}
}
