package config

import (
	"fmt"
	"strings"

	"github.com/rickymw/MotorHome/internal/usbdev"
)

type Config struct {
	Driver       string `json:"driver"`       // iRacing UserName used by lapanalyze to identify the player's car
	IbtDir       string `json:"ibtDir"`       // directory to search for .ibt files when none is specified on the command line
	Hotkey       string `json:"hotkey"`       // key name for voice notes, e.g. "F13", "ScrollLock", "0x91"
	WhisperPath  string `json:"whisperPath"`  // path to whisper-cli.exe (absolute, or relative to the binary)
	WhisperModel string `json:"whisperModel"` // path to whisper .bin model file (e.g. ggml-base.en.bin)
	Apps         []App  `json:"apps"`

	// USBDevices is the sim-racing hardware `usb` recognises. When empty, the
	// built-in list in usbdev.KnownDevices is used; when non-empty it
	// *replaces* that list rather than adding to it, so a device you do not own
	// can be removed. See usbdev.ResolveKnown.
	//
	// omitempty so an existing config file that has never named a device does
	// not grow a null field the first time it is saved.
	USBDevices []USBDevice `json:"usbDevices,omitempty"`
}

// USBDevice identifies one piece of sim-racing hardware by USB vendor/product
// ID.
//
// VID and PID are hex *strings* ("0x30B7") rather than numbers, because that is
// how they appear in Device Manager and in a device instance ID, and a config
// file someone may hand-edit should hold the form they will be copying from.
// JSON has no hex literal, so storing them as numbers would mean writing 12471
// for VID_30B7 — unrecognisable next to the source it came from.
type USBDevice struct {
	Alias string `json:"alias"` // short name used on the command line
	Name  string `json:"name"`  // human-readable product name
	VID   string `json:"vid"`
	PID   string `json:"pid"`
}

type App struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Args        string `json:"args"`
	WindowStyle string `json:"windowStyle"`
	DelayMs     int    `json:"delayMs"`
	Elevate     bool   `json:"elevate"`
	ProcessName string `json:"processName"`
}

var validWindowStyles = map[string]bool{
	"":       true,
	"normal": true,
	"hidden": true,
}

func (cfg Config) Validate() error {
	for i, app := range cfg.Apps {
		if app.Name == "" {
			return fmt.Errorf("app[%d]: name is required", i)
		}
		if app.Path == "" {
			return fmt.Errorf("app %q: path is required", app.Name)
		}
		if app.DelayMs < 0 {
			return fmt.Errorf("app %q: delayMs must be >= 0, got %d", app.Name, app.DelayMs)
		}
		if !validWindowStyles[strings.ToLower(app.WindowStyle)] {
			return fmt.Errorf("app %q: invalid windowStyle %q (valid: Normal, Hidden)", app.Name, app.WindowStyle)
		}
	}
	return cfg.validateUSBDevices()
}

// validateUSBDevices checks the device list.
//
// It parses the IDs with the same usbdev.ParseHexID the conversion uses rather
// than a lookalike check, because the property that matters is that Validate
// never accepts a document the conversion would then reject — the GUI's settings
// panel writes only what Validate passes, and a config that saves but does not
// load is the one failure that panel must not have.
func (cfg Config) validateUSBDevices() error {
	seenAlias := make(map[string]bool, len(cfg.USBDevices))
	seenID := make(map[[2]uint16]string, len(cfg.USBDevices))

	for i, d := range cfg.USBDevices {
		alias := strings.TrimSpace(d.Alias)
		if alias == "" {
			return fmt.Errorf("usbDevices[%d]: alias is required", i)
		}
		// The alias is a command-line target, so a leading dash would be read
		// as a flag and whitespace would never match what the user typed.
		if alias != d.Alias {
			return fmt.Errorf("usbDevices[%d]: alias %q has leading or trailing whitespace", i, d.Alias)
		}
		if strings.HasPrefix(alias, "-") {
			return fmt.Errorf("usbDevices[%d]: alias %q must not start with a dash", i, alias)
		}
		// "all" is the wildcard target, so a device called that could never be
		// addressed on its own.
		if strings.EqualFold(alias, "all") {
			return fmt.Errorf(`usbDevices[%d]: "all" is reserved — it is the wildcard target`, i)
		}
		if seenAlias[strings.ToLower(alias)] {
			return fmt.Errorf("usbDevices[%d]: duplicate alias %q", i, alias)
		}
		seenAlias[strings.ToLower(alias)] = true

		if strings.TrimSpace(d.Name) == "" {
			return fmt.Errorf("usbDevices[%d] (%s): name is required", i, alias)
		}

		vid, err := usbdev.ParseHexID(d.VID)
		if err != nil {
			return fmt.Errorf("usbDevices[%d] (%s): vid %v", i, alias, err)
		}
		pid, err := usbdev.ParseHexID(d.PID)
		if err != nil {
			return fmt.Errorf("usbDevices[%d] (%s): pid %v", i, alias, err)
		}

		// Two aliases on one VID/PID would both match the same physical device,
		// so `usb off <either>` would toggle it and the listing would show it
		// twice in different states.
		if prev, dup := seenID[[2]uint16{vid, pid}]; dup {
			return fmt.Errorf("usbDevices[%d] (%s): same vid/pid as %q — one device cannot have two aliases", i, alias, prev)
		}
		seenID[[2]uint16{vid, pid}] = alias
	}
	return nil
}

// KnownUSBDevices converts the config's device list into the form usbdev takes.
// Validate has already guaranteed the IDs parse, so a bad entry here is
// impossible rather than merely unlikely — it is skipped instead of panicking.
func (cfg Config) KnownUSBDevices() []usbdev.Known {
	out := make([]usbdev.Known, 0, len(cfg.USBDevices))
	for _, d := range cfg.USBDevices {
		vid, errV := usbdev.ParseHexID(d.VID)
		pid, errP := usbdev.ParseHexID(d.PID)
		if errV != nil || errP != nil {
			continue
		}
		out = append(out, usbdev.Known{Alias: d.Alias, Name: d.Name, VID: vid, PID: pid})
	}
	return out
}
