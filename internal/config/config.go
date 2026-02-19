package config

type Config struct {
	LogFile string `json:"logFile"`
	Apps    []App  `json:"apps"`
}

type App struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Args        string `json:"args"`
	WindowStyle string `json:"windowStyle"`
	DelayMs     int    `json:"delayMs"`
	Elevate     bool   `json:"elevate"`
	ProcessName string `json:"processName"`
	Close       Close  `json:"close"`
}

type Close struct {
	Method      string `json:"method"`
	ProcessName string `json:"processName"`
	TimeoutMs   int    `json:"timeoutMs"`
}
