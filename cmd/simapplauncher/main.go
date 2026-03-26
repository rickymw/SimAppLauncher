package main

import (
	"fmt"
	"os"

	"github.com/rickymw/SimAppLauncher/internal/config"
	"github.com/rickymw/SimAppLauncher/internal/launcher"
)

const configPath = "launcher.config.json"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	pm := launcher.NewProcessManager()
	switch os.Args[1] {
	case "start":
		launcher.RunStart(cfg, pm)
	case "stop":
		launcher.RunStop(cfg, pm)
	case "status":
		launcher.RunStatus(cfg, pm)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: simapplauncher <start|stop|status>")
}
