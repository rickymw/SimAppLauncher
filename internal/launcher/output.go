package launcher

import "fmt"

func PrintLaunched(name string, pid int) {
	fmt.Printf("  [+] %-20s ... launched (pid %d)\n", name, pid)
}

func PrintFailed(name, reason string) {
	fmt.Printf("  [!] %-20s ... FAILED: %s\n", name, reason)
}

func PrintAlreadyRunning(name string, pid int) {
	fmt.Printf("  [=] %-20s ... already running (pid %d)\n", name, pid)
}

func PrintClosed(name string) {
	fmt.Printf("  [-] %-20s ... closed\n", name)
}

func PrintStatus(name string, running bool, pid int) {
	state := "STOPPED"
	pidStr := "-"
	if running {
		state = "RUNNING"
		pidStr = fmt.Sprintf("%d", pid)
	}
	fmt.Printf("  %-20s %-8s %s\n", name, state, pidStr)
}
