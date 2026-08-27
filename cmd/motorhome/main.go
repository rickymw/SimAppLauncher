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
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] <start|stop|status|analyze|coach|pb|notes|live|camera|usb|gui>")
		fmt.Fprintln(os.Stderr, "       motorhome analyze [-lap N] [-update-map] [-json] [-dump T3 [-dump-all]] [file.ibt]")
		fmt.Fprintln(os.Stderr, "       motorhome coach [-lap N] [-table] [file.ibt]")
		fmt.Fprintln(os.Stderr, "       motorhome pb [list|show|diff|prune]")
		fmt.Fprintln(os.Stderr, "       motorhome notes [set-hotkey]")
		fmt.Fprintln(os.Stderr, "       motorhome live [-watch] [-hz N] [-raw]")
		fmt.Fprintln(os.Stderr, "       motorhome camera")
		fmt.Fprintln(os.Stderr, "       motorhome gui [-port N] [-no-open]")
		fmt.Fprintln(os.Stderr, "       motorhome usb [list|scan] [-v]")
		fmt.Fprintf(os.Stderr, "       motorhome usb <on|off|toggle> <%s>   (`usb list` names your devices)\n", usbTargetHint())
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(1)
	}

	// camera and usb read nothing from the config, and are the subcommands most
	// likely to run from a bare copy of the exe — un-sticking a webcam
	// redirected into an RDP session means running it on the far end, where
	// there is no launcher.config.json, and usb re-runs this exe elevated,
	// where the working directory is not the user's. Dispatch both before the
	// config load so a missing config can't block them.
	switch args[0] {
	case "camera":
		RunCamera(args[1:])
		return
	case "usb":
		os.Exit(RunUSB(args[1:], *cfgPath))
	case "gui":
		// Also dispatched ahead of the config load, but for a different reason
		// than camera and usb: the GUI's settings panel exists to *fix* the
		// config. Failing here would mean a malformed launcher.config.json
		// locks the user out of the one screen that can repair it. The server
		// re-reads the config on every request anyway, so a load failure is
		// reported per-panel and the settings form can still save a good one
		// over it.
		RunGUI(args[1:], *cfgPath, filepath.Join(filepath.Dir(*cfgPath), "pb.json"))
		return
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
