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
	Alias      string `json:"alias"`
	Name       string `json:"name"`
	State      string `json:"state"`
	InstanceID string `json:"instanceId,omitempty"`
	// Actionable is false for a device that is not plugged in. The page uses it
	// to disable the buttons rather than letting the user issue a toggle that
	// can only come back as "nothing to do".
	Actionable bool `json:"actionable"`
}

type usbResponse struct {
	Devices []usbDevice `json:"devices"`
	// Output carries the subcommand's own per-device lines after a change, so
	// the panel can show exactly what the CLI would have printed — including
	// the "a game may need restarting to notice" note, which is real advice
	// that would be lost if only the new states were returned.
	Output string `json:"output,omitempty"`
}

func (s *Server) handleUSBList(w http.ResponseWriter, r *http.Request) {
	if s.deps.USB == nil {
		unsupported(w, "USB device control")
		return
	}
	devs, err := s.deps.USB.Enumerate()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot enumerate USB devices: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usbResponse{Devices: toUSBDevices(devs)})
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
	devs, err := s.deps.USB.Enumerate()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "device change reported: "+strings.TrimSpace(string(out))+
			"; but the device list could not be re-read: "+err.Error())
		return
	}

	resp := usbResponse{Devices: toUSBDevices(devs), Output: strings.TrimSpace(string(out))}
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
