//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteExW  = shell32.NewProc("ShellExecuteExW")
	kernel32Elev         = syscall.NewLazyDLL("kernel32.dll")
	procGetExitCodeProc  = kernel32Elev.NewProc("GetExitCodeProcess")
	procWaitForSingleObj = kernel32Elev.NewProc("WaitForSingleObject")
)

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	seeMaskFlagNoUI       = 0x00000400

	swHide = 0

	infiniteWait = 0xFFFFFFFF

	// tokenElevation is TokenElevation, the TOKEN_INFORMATION_CLASS that
	// reports whether the process token is the full or the filtered one.
	tokenElevation = 20
)

// shellExecuteInfo mirrors SHELLEXECUTEINFOW. Field order and widths must match
// exactly — the struct is passed by pointer and Windows reads it positionally.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIconMonitor uintptr
	hProcess     uintptr
}

// winIsElevated reports whether this process holds a full administrator token.
//
// Group membership is not the question. On this machine the user *is* in the
// Administrators group but normal processes still run with the filtered token,
// so a membership check would report true for a process that cannot change a
// device state.
func winIsElevated() bool {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer token.Close()

	var isElevated uint32
	var returned uint32
	err = syscall.GetTokenInformation(
		token,
		tokenElevation,
		(*byte)(unsafe.Pointer(&isElevated)),
		uint32(unsafe.Sizeof(isElevated)),
		&returned,
	)
	if err != nil {
		return false
	}
	return isElevated != 0
}

// winRelaunchElevated re-runs this executable with the given arguments under an
// elevated token, waits for it, and returns its exit code.
//
// The child is hidden and writes nothing to a console of its own — an elevated
// process gets a fresh console that the parent cannot read — so callers pass
// -elevated-out and read the result back from that file.
func winRelaunchElevated(args []string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("locating own executable: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, syscall.EscapeArg(a))
	}

	verb, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return 0, err
	}
	file, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	params, err := syscall.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return 0, err
	}
	var dir *uint16
	if cwd != "" {
		dir, _ = syscall.UTF16PtrFromString(cwd)
	}

	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: params,
		lpDirectory:  dir,
		nShow:        swHide,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	ret, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		return 0, fmt.Errorf("elevation failed: %w", callErr)
	}
	if info.hProcess == 0 {
		return 0, fmt.Errorf("elevation returned no process handle")
	}
	defer syscall.CloseHandle(syscall.Handle(info.hProcess))

	procWaitForSingleObj.Call(info.hProcess, uintptr(infiniteWait))

	var code uint32
	ok, _, callErr := procGetExitCodeProc.Call(info.hProcess, uintptr(unsafe.Pointer(&code)))
	if ok == 0 {
		return 0, fmt.Errorf("reading elevated exit code: %w", callErr)
	}
	return int(code), nil
}
