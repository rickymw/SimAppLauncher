//go:build windows

package main

import (
	"github.com/rickymw/MotorHome/internal/camera"
	"github.com/rickymw/MotorHome/internal/gui"
	"github.com/rickymw/MotorHome/internal/iracing"
	"github.com/rickymw/MotorHome/internal/usbdev"
)

// attachPlatformDeps fills in the three providers that only exist on Windows:
// iRacing shared memory, SetupAPI device enumeration, and the service control
// manager. internal/gui stays free of all three so it compiles and tests on any
// OS; this file is the only place they meet.
func attachPlatformDeps(deps *gui.Deps) {
	deps.Live = liveProvider{}
	deps.USB = usbProvider{}
	deps.Camera = camera.NewRestarter()
}

// usbProvider builds a controller per call rather than holding one.
//
// The device list is now editable through the settings panel, and a controller
// constructed once at startup would keep matching against whatever the server
// booted with — a device added through the picker would not show up until a
// restart, which is exactly the friction the picker exists to remove.
// Construction is a struct literal; there is nothing to reuse.
type usbProvider struct{}

func (usbProvider) Enumerate(known []usbdev.Known) ([]usbdev.Device, error) {
	return usbdev.NewController(known).Enumerate()
}

func (usbProvider) Scan(known []usbdev.Known) ([]usbdev.Scanned, error) {
	return usbdev.NewController(known).Scan()
}

type liveProvider struct{}

// Snapshot reduces a shared-memory read to the same view the `live` subcommand
// prints.
//
// It calls gapsFromLive — the helper live.go already uses — rather than
// recomputing which car is ahead. That function encodes decisions that are not
// obvious (shortest on-track distance rather than race position, the EstTime
// fallback when two cars straddle the S/F line), and a second implementation
// would eventually disagree with the terminal about the same moment.
func (liveProvider) Snapshot() gui.LiveSnapshot {
	ld := iracing.ReadLiveData()
	if !ld.Connected {
		// The Win32 reason goes in Detail rather than becoming the message.
		// Not connected is overwhelmingly "the sim is not running", and
		// "OpenFileMappingW: The system cannot find the file specified" is a
		// alarming way to say so on a panel someone glances at mid-session.
		return gui.LiveSnapshot{
			Connected: false,
			Message:   "iRacing is not running, or you are not on track",
			Detail:    ld.ErrMsg,
		}
	}

	snap := gui.LiveSnapshot{
		Connected:  true,
		Track:      ld.Track,
		Car:        ld.Car,
		LapDistPct: ld.LapDistPct,
		FieldSize:  countValidCars(ld.CarIdxLapDistPct),
	}

	if ld.MyCarIdx >= 0 {
		if p := idxValue(ld.CarIdxPosition, ld.MyCarIdx); p > 0 {
			snap.Position = int(p)
		}
		if cp := idxValue(ld.CarIdxClassPosition, ld.MyCarIdx); cp > 0 {
			snap.ClassPosition = int(cp)
		}
		// iRacing publishes -1 before the first S/F crossing; that is the out
		// lap, which the driver counts as lap 1.
		if lc := idxValue(ld.CarIdxLapCompleted, ld.MyCarIdx); lc < 0 {
			snap.Lap = 1
		} else {
			snap.Lap = int(lc + 1)
		}
		if mine, ok := ld.Drivers[ld.MyCarIdx]; ok && mine.CarClassID != 0 {
			for _, d := range ld.Drivers {
				if d.CarClassID == mine.CarClassID {
					snap.ClassSize++
				}
			}
		}
	}

	ahead, behind := gapsFromLive(ld)
	snap.Ahead = toLiveGap(ld, ahead)
	snap.Behind = toLiveGap(ld, behind)
	return snap
}

// toLiveGap converts one neighbour, returning nil when there is nobody in that
// direction. nil rather than a zero struct so the browser can tell "solo
// session" from "a car exactly alongside".
func toLiveGap(ld iracing.LiveData, g iracing.GapTo) *gui.LiveGap {
	if g.CarIdx < 0 {
		return nil
	}
	out := &gui.LiveGap{
		TimeSeconds: g.TimeSeconds,
		LapsDelta:   int(g.LapsDelta),
	}
	if d, ok := ld.Drivers[g.CarIdx]; ok {
		out.DriverName = d.UserName
		out.CarNumber = d.CarNumber
	}
	return out
}
