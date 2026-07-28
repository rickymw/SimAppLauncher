package main

import (
	"github.com/rickymw/MotorHome/internal/camera"
	"github.com/rickymw/MotorHome/internal/config"
)

// RunCamera restarts the Windows Camera Frame Server, clearing a stuck or
// frozen webcam without requiring an elevated process.
func RunCamera(_ []string, _ config.Config) {
	camera.RunCameraRestart(camera.NewRestarter())
}
