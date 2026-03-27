//go:build windows

package launcher

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/rickymw/SimAppLauncher/internal/config"
)

var (
	advapi32             = syscall.NewLazyDLL("advapi32.dll")
	procOpenProcess      = kernel32.NewProc("OpenProcess")
	procTerminateProcess = kernel32.NewProc("TerminateProcess")
	procOpenProcessToken = advapi32.NewProc("OpenProcessToken")
	procLookupPrivValue  = advapi32.NewProc("LookupPrivilegeValueW")
	procAdjustTokenPrivs = advapi32.NewProc("AdjustTokenPrivileges")
)

const (
	processTerminate        = 0x0001
	tokenAdjustPrivileges   = 0x0020
	tokenQuery              = 0x0008
	sePrivilegeEnabled      = 0x0002
)

type luid struct {
	LowPart  uint32
	HighPart int32
}

type luidAndAttributes struct {
	Luid       luid
	Attributes uint32
}

type tokenPrivileges struct {
	PrivilegeCount uint32
	Privileges     [1]luidAndAttributes
}

type windowsProcessManager struct{}

// NewProcessManager returns the Windows implementation of ProcessManager.
func NewProcessManager() ProcessManager {
	return &windowsProcessManager{}
}

func (w *windowsProcessManager) Spawn(app config.App) SpawnResult {
	if app.Elevate {
		return spawnElevated(app)
	}

	args := strings.Fields(app.Args)
	cmd := exec.Command(app.Path, args...)

	// Hidden: suppress window entirely via HideWindow.
	// Minimized: requires golang.org/x/sys/windows for StartupInfo; treated as Normal for now.
	hideWindow := strings.ToLower(app.WindowStyle) == "hidden"
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: hideWindow,
	}

	if err := cmd.Start(); err != nil {
		return SpawnResult{Err: err}
	}
	return SpawnResult{PID: cmd.Process.Pid}
}

func (w *windowsProcessManager) IsRunning(processName string) (pid int, running bool, err error) {
	out, cmdErr := tasklistOutput(processName)
	if cmdErr != nil {
		return 0, false, cmdErr
	}
	p, parseErr := parsePIDFromTasklist(out, processName)
	if parseErr != nil {
		return 0, false, nil // not found — not an error for IsRunning
	}
	return p, true, nil
}

func (w *windowsProcessManager) Kill(processName string) error {
	// Try standard taskkill first.
	if err := exec.Command("taskkill", "/F", "/IM", processName+".exe").Run(); err == nil {
		return nil
	}
	// taskkill failed — check whether the process is actually running before
	// attempting the SeDebugPrivilege path. If it is already stopped, return
	// success so that RunStop does not report spurious failures.
	if _, running, err := w.IsRunning(processName); err == nil && !running {
		return nil
	}
	// Still running (or status check failed) — try SeDebugPrivilege fallback.
	return killWithDebugPrivilege(processName)
}

// tasklistOutput runs tasklist filtered to processName and returns the raw output.
func tasklistOutput(processName string) (string, error) {
	filter := fmt.Sprintf("IMAGENAME eq %s.exe", processName)
	out, err := exec.Command("tasklist", "/FI", filter, "/NH", "/FO", "CSV").Output()
	if err != nil {
		return "", fmt.Errorf("tasklist failed: %w", err)
	}
	return string(out), nil
}

// killWithDebugPrivilege enables SeDebugPrivilege on the current process token,
// then opens and terminates the target process via the Windows API.
func killWithDebugPrivilege(processName string) error {
	if err := enableDebugPrivilege(); err != nil {
		return fmt.Errorf("could not enable SeDebugPrivilege: %w", err)
	}

	out, err := tasklistOutput(processName)
	if err != nil {
		return err
	}
	pid, err := parsePIDFromTasklist(out, processName)
	if err != nil {
		return err
	}

	handle, _, err := procOpenProcess.Call(processTerminate, 0, uintptr(pid))
	if handle == 0 {
		return fmt.Errorf("OpenProcess failed: %w", err)
	}
	defer syscall.CloseHandle(syscall.Handle(handle))

	ret, _, err := procTerminateProcess.Call(handle, 1)
	if ret == 0 {
		return fmt.Errorf("TerminateProcess failed: %w", err)
	}
	return nil
}

// enableDebugPrivilege adds SeDebugPrivilege to the current process token.
func enableDebugPrivilege() error {
	var token syscall.Token
	proc, err := syscall.GetCurrentProcess()
	if err != nil {
		return err
	}
	if r, _, e := procOpenProcessToken.Call(uintptr(proc), tokenAdjustPrivileges|tokenQuery, uintptr(unsafe.Pointer(&token))); r == 0 {
		return fmt.Errorf("OpenProcessToken: %w", e)
	}
	defer token.Close()

	name, _ := syscall.UTF16PtrFromString("SeDebugPrivilege")
	var id luid
	if r, _, e := procLookupPrivValue.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&id))); r == 0 {
		return fmt.Errorf("LookupPrivilegeValue: %w", e)
	}

	tp := tokenPrivileges{
		PrivilegeCount: 1,
		Privileges: [1]luidAndAttributes{{
			Luid:       id,
			Attributes: sePrivilegeEnabled,
		}},
	}
	if r, _, e := procAdjustTokenPrivs.Call(uintptr(token), 0, uintptr(unsafe.Pointer(&tp)), uintptr(unsafe.Sizeof(tp)), 0, 0); r == 0 {
		return fmt.Errorf("AdjustTokenPrivileges: %w", e)
	}
	return nil
}

// parsePIDFromTasklist extracts the first PID matching processName from tasklist CSV output.
func parsePIDFromTasklist(output, processName string) (int, error) {
	lowerName := strings.ToLower(processName + ".exe")
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "\""+lowerName+"\"") {
			fields := strings.Split(strings.TrimSpace(line), ",")
			if len(fields) >= 2 {
				if p, err := strconv.Atoi(strings.Trim(fields[1], "\"")); err == nil {
					return p, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("process %q not found", processName)
}
