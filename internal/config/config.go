package config

import (
	"fmt"
	"strings"
)

type Config struct {
	Driver       string `json:"driver"`       // iRacing UserName used by lapanalyze to identify the player's car
	IbtDir       string `json:"ibtDir"`       // directory to search for .ibt files when none is specified on the command line
	Hotkey       string `json:"hotkey"`       // key name for voice notes, e.g. "F13", "ScrollLock", "0x91"
	WhisperPath  string `json:"whisperPath"`  // path to whisper-cli.exe (absolute, or relative to the binary)
	WhisperModel string `json:"whisperModel"` // path to whisper .bin model file (e.g. ggml-base.en.bin)
	Apps         []App  `json:"apps"`
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
	return nil
}
