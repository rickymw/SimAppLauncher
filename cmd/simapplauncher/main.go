package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rickymw/SimAppLauncher/internal/config"
	"github.com/rickymw/SimAppLauncher/internal/launcher"
)

func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "launcher.config.json"
	}
	return filepath.Join(filepath.Dir(exe), "launcher.config.json")
}

func main() {
	cfgPath := flag.String("config", defaultConfigPath(), "path to config file")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: simapplauncher [-config <path>] <start|stop|status>")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(1)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	pm := launcher.NewProcessManager()
	switch args[0] {
	case "start":
		launcher.RunStart(cfg, pm)
	case "stop":
		launcher.RunStop(cfg, pm)
	case "status":
		launcher.RunStatus(cfg, pm)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		flag.Usage()
		os.Exit(1)
	}
}
