package gui

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rickymw/MotorHome/internal/usbdev"
)

// usbTimeout bounds one device toggle. The elevation itself is silent on this
// rig, but SetupDiCallClassInstaller can sit for several seconds per device and
// `off all` walks the whole list.
const usbTimeout = 2 * time.Minute

// cameraTimeout matches the camera package's own stopTimeout plus room for the
// start half. Windows waits for a stuck client to release the device, which is
// where nearly all of that goes.
const cameraTimeout = 2 * time.Minute

type usbDevice struct {
	Alias string `json:"alias"`
	Name  string `json:"name"`
	State string `json:"state"`
	// VID/PID are carried in the config's hex-string form so the page can seed
	// a settings document from the current list without converting anything.
	VID        string `json:"vid"`
	PID        string `json:"pid"`
	InstanceID string `json:"instanceId,omitempty"`
	// Actionable is false for a device that is not plugged in. The page uses it
	// to disable the buttons rather than letting the user issue a toggle that
	// can only come back as "nothing to do".
	Actionable bool `json:"actionable"`
}

// scannedDevice is one USB device as the picker sees it. VID and PID are
// pre-formatted as the hex strings the config stores, so adding a device is a
// copy of what the scan showed rather than a conversion the page has to get
// right.
type scannedDevice struct {
	HardwareID string `json:"hardwareId"`
	VID        string `json:"vid"`
	PID        string `json:"pid"`
	Desc       string `json:"desc,omitempty"`
	State      string `json:"state"`
	InstanceID string `json:"instanceId,omitempty"`
	// Alias and Name are set when the device is already in the list; Known
	// says so directly, so the page does not have to infer it from an empty
	// string.
	Alias string `json:"alias,omitempty"`
	Name  string `json:"name,omitempty"`
	Known bool   `json:"known"`
	// Count is how many devnodes share this hardware ID. Above one, adding this
	// row to the device list claims all of them — which the picker has to say,
	// because it changes what the Add button means.
	Count int `json:"count"`
}

type scanResponse struct {
	Devices []scannedDevice `json:"devices"`
}

type usbResponse struct {
	Devices []usbDevice `json:"devices"`
	// FromConfig says the list came from usbDevices in the config rather than
	// the built-in defaults. Disclosed for the same reason the CLI table
	// discloses it: a configured list replaces the built-ins, so someone who
	// added one device needs to see why the others went away.
	FromConfig bool `json:"fromConfig"`
	// Output carries the subcommand's own per-device lines after a change, so
	// the panel can show exactly what the CLI would have printed — including
	// the "a game may need restarting to notice" note, which is real advice
	// that would be lost if only the new states were returned.
	Output string `json:"output,omitempty"`
}

// knownUSB reads the device list from the current config. An unreadable config
// yields nil, which usbdev.ResolveKnown turns into the built-in rig — the same
// fallback the CLI takes, so a broken config never leaves the panel with no
// devices at all.
func (s *Server) knownUSB() (known []usbdev.Known, fromConfig bool) {
	cfg, err := s.deps.LoadConfig()
	if err != nil {
		return nil, false
	}
	k := cfg.KnownUSBDevices()
	return k, len(k) > 0
}

func (s *Server) handleUSBList(w http.ResponseWriter, r *http.Request) {
	if s.deps.USB == nil {
		unsupported(w, "USB device control")
		return
	}
	known, fromConfig := s.knownUSB()
	devs, err := s.deps.USB.Enumerate(known)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot enumerate USB devices: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usbResponse{Devices: toUSBDevices(devs), FromConfig: fromConfig})
}

// handleUSBScan reports every USB device on the machine so the picker can offer
// the ones not yet claimed.
//
// This is the read that makes the device list configurable in practice rather
// than only in principle: the alternative is the user finding a VID/PID in
// Device Manager and typing it into a form, which is most of the work the
// hardcoded list was avoiding.
func (s *Server) handleUSBScan(w http.ResponseWriter, r *http.Request) {
	if s.deps.USB == nil {
		unsupported(w, "USB device control")
		return
	}
	known, _ := s.knownUSB()
	found, err := s.deps.USB.Scan(known)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot scan USB devices: "+err.Error())
		return
	}

	out := make([]scannedDevice, 0, len(found))
	for _, f := range found {
		out = append(out, scannedDevice{
			HardwareID: f.HardwareID(),
			VID:        usbdev.FormatHexID(f.VID),
			PID:        usbdev.FormatHexID(f.PID),
			Desc:       f.Desc,
			State:      f.State.String(),
			InstanceID: f.InstanceID,
			Alias:      f.Alias,
			Name:       f.Name,
			Known:      f.IsKnown(),
			Count:      f.Count,
		})
	}
	writeJSON(w, http.StatusOK, scanResponse{Devices: out})
}

type usbSetRequest struct {
	Action string `json:"action"` // on | off | toggle
	Target string `json:"target"` // alias, unambiguous substring, or "all"
}

// handleUSBSet changes a device state by shelling out to the `usb` subcommand.
//
// It does not call usbdev.Controller.SetEnabled directly, even though the
// provider is right there, because that call needs a full administrator token
// and this process does not have one. The subcommand already knows how to
// re-exec itself under UAC and read the elevated child's output back; going
// through it means the browser path and the Stream Deck path elevate the same
// way, and there is only one place where that has to be right.
func (s *Server) handleUSBSet(w http.ResponseWriter, r *http.Request) {
	if s.deps.USB == nil {
		unsupported(w, "USB device control")
		return
	}
	if s.deps.RunSubcommand == nil {
		writeErr(w, http.StatusNotImplemented, "device changes are not available in this build")
		return
	}

	var req usbSetRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}

	switch req.Action {
	case "on", "off", "toggle":
	default:
		writeErr(w, http.StatusBadRequest, "action must be on, off or toggle")
		return
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		writeErr(w, http.StatusBadRequest, "no device named")
		return
	}
	// The target reaches a command line, so a leading dash would be parsed as a
	// flag by the subcommand's flag set rather than as a device name.
	if strings.HasPrefix(target, "-") {
		writeErr(w, http.StatusBadRequest, "device name must not start with a dash")
		return
	}

	out, runErr := s.deps.RunSubcommand(usbTimeout, "usb", req.Action, target)

	// Re-enumerate either way. A partial failure — one device toggled, the next
	// refused — still changed the rig, and a panel left showing the old states
	// after that is worse than one showing the new ones next to the error.
	known, fromConfig := s.knownUSB()
	devs, err := s.deps.USB.Enumerate(known)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "device change reported: "+strings.TrimSpace(string(out))+
			"; but the device list could not be re-read: "+err.Error())
		return
	}

	resp := usbResponse{Devices: toUSBDevices(devs), FromConfig: fromConfig, Output: strings.TrimSpace(string(out))}
	if runErr != nil {
		if resp.Output == "" {
			resp.Output = runErr.Error()
		}
		writeJSON(w, http.StatusConflict, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func toUSBDevices(devs []usbdev.Device) []usbDevice {
	out := make([]usbDevice, 0, len(devs))
	for _, d := range devs {
		out = append(out, usbDevice{
			Alias:      d.Alias,
			Name:       d.Name,
			State:      d.State.String(),
			VID:        usbdev.FormatHexID(d.VID),
			PID:        usbdev.FormatHexID(d.PID),
			InstanceID: d.InstanceID,
			Actionable: d.State != usbdev.StateAbsent,
		})
	}
	return out
}

type cameraResponse struct {
	Services []cameraService `json:"services"`
	// Progress carries the lines the restarter emitted while it waited. A
	// restart that blocked for 30s on a stuck client explains itself here; the
	// CLI prints these as they happen, and the browser gets them at the end
	// because the request is what the page is waiting on.
	Progress  []string `json:"progress,omitempty"`
	Restarted int      `json:"restarted"`
}

type cameraService struct {
	Name      string `json:"name"`
	Restarted bool   `json:"restarted"`
}

func (s *Server) handleCamera(w http.ResponseWriter, r *http.Request) {
	if s.deps.Camera == nil {
		unsupported(w, "camera restart")
		return
	}

	// Serialised: two overlapping restarts would have one stopping the services
	// the other is starting.
	s.cameraMu.Lock()
	defer s.cameraMu.Unlock()

	var progress []string
	results, err := s.deps.Camera.Restart(func(msg string) {
		progress = append(progress, msg)
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := cameraResponse{Progress: progress}
	for _, res := range results {
		resp.Services = append(resp.Services, cameraService{Name: res.Name, Restarted: res.Restarted})
		if res.Restarted {
			resp.Restarted++
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
