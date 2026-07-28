package camera

import "fmt"

// ServiceResult reports what happened to one camera-pipeline service.
type ServiceResult struct {
	Name      string
	Restarted bool // false = was already stopped, so it was left alone
}

// Restarter restarts the OS camera pipeline so it can be mocked in tests.
type Restarter interface {
	Restart() ([]ServiceResult, error)
}

// RunCameraRestart restarts the camera pipeline via r and prints what it did.
func RunCameraRestart(r Restarter) {
	results, err := r.Restart()
	if err != nil {
		fmt.Printf("  [!] FAILED: %v\n", err)
		return
	}

	restarted := 0
	for _, res := range results {
		if res.Restarted {
			fmt.Printf("  [+] %-20s ... restarted\n", res.Name)
			restarted++
		} else {
			fmt.Printf("  [=] %-20s ... already stopped\n", res.Name)
		}
	}

	if restarted == 0 {
		fmt.Println("\nCamera pipeline was not running — nothing to restart.")
		fmt.Println("Windows starts it on demand, so the camera will initialise fresh on next use.")
		return
	}
	fmt.Printf("\nDone. %d/%d services restarted.\n", restarted, len(results))
}
