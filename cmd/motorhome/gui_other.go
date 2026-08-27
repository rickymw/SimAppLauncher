//go:build !windows

package main

import "github.com/rickymw/MotorHome/internal/gui"

// attachPlatformDeps is a no-op off Windows: shared memory, SetupAPI and the
// service control manager have no counterpart there. The providers stay nil and
// gui reports 501 for those panels, which is what lets `go test ./...` and a
// non-Windows build of the rest of the interface work at all.
func attachPlatformDeps(_ *gui.Deps) {}
