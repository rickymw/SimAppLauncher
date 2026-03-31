package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/launcher"
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
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] <start|stop|status|analyze|notes>")
		fmt.Fprintln(os.Stderr, "       motorhome analyze [-lap N] [-compare N,M] <file.ibt>")
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

	trackmapPath := filepath.Join(filepath.Dir(*cfgPath), "trackmap.json")
	pbPath := filepath.Join(filepath.Dir(*cfgPath), "pb.json")
	notesDir := filepath.Join(filepath.Dir(*cfgPath), "notes")

	switch args[0] {
	case "analyze":
		RunAnalyze(args[1:], cfg, trackmapPath, pbPath)
	case "notes":
		RunNotes(args[1:], cfg, notesDir, *cfgPath, trackmapPath)
	default:
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
}
