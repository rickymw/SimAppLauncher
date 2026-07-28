package camera

import "fmt"

// ServiceResult reports what happened to one camera-pipeline service.
type ServiceResult struct {
	Name      string
	Restarted bool // false = was already stopped, so it was left alone
}

// Restarter restarts the OS camera pipeline so it can be mocked in tests.
// progress, when non-nil, is called with status lines during slow operations —
// stopping the pipeline blocks until the app holding the camera releases it,
// which can take ~30s, and the user should be told why rather than left staring
// at an apparently hung window.
type Restarter interface {
	Restart(progress func(string)) ([]ServiceResult, error)
}

// RunCameraRestart restarts the camera pipeline via r and prints what it did.
func RunCameraRestart(r Restarter) {
	results, err := r.Restart(func(msg string) { fmt.Println(msg) })
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
