package config

import (
	"fmt"
	"strings"
)

type Config struct {
	Apps []App `json:"apps"`
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
