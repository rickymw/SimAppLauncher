package main

import (
	"github.com/rickymw/MotorHome/internal/camera"
)

// RunCamera restarts the Windows Camera Frame Server, clearing a stuck or
// frozen webcam without requiring an elevated process.
//
// It deliberately takes no config: nothing here is configurable, and the
// subcommand needs to run from a bare copy of the exe on a machine that has no
// launcher.config.json — clearing a camera redirected into an RDP session means
// running it on the far end. main dispatches it before loading the config for
// that reason.
func RunCamera(_ []string) {
	camera.RunCameraRestart(camera.NewRestarter())
}
