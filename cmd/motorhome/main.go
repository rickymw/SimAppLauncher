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
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] <start|stop|status|analyze|coach|pb|notes|live|camera>")
		fmt.Fprintln(os.Stderr, "       motorhome analyze [-lap N] [-update-map] [-json] [-dump T3 [-dump-all]] [file.ibt]")
		fmt.Fprintln(os.Stderr, "       motorhome coach [-lap N] [file.ibt]")
		fmt.Fprintln(os.Stderr, "       motorhome pb [list|show|diff|prune]")
		fmt.Fprintln(os.Stderr, "       motorhome notes [set-hotkey]")
		fmt.Fprintln(os.Stderr, "       motorhome live [-watch] [-hz N] [-raw]")
		fmt.Fprintln(os.Stderr, "       motorhome camera")
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
		finish, err := captureStdout()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: clipboard capture disabled: %v\n", err)
			RunAnalyze(args[1:], cfg, trackmapPath, pbPath, notesDir)
			return
		}
		RunAnalyze(args[1:], cfg, trackmapPath, pbPath, notesDir)
		out := finish()
		if err := copyToClipboard(out); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not copy to clipboard: %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "(copied to clipboard)")
		}
	case "coach":
		RunCoach(args[1:], cfg, trackmapPath, pbPath, notesDir, *cfgPath)
	case "pb":
		RunPB(args[1:], cfg, pbPath)
	case "notes":
		RunNotes(args[1:], cfg, notesDir, *cfgPath)
	case "live":
		RunLive(args[1:], cfg)
	case "camera":
		RunCamera(args[1:], cfg)
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
