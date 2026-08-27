package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/gui"
	"github.com/rickymw/MotorHome/internal/launcher"
)

// defaultGUIPort is high enough to need no privileges and specific enough not
// to collide with the usual local dev servers (3000/5173/8080).
const defaultGUIPort = 7777

// RunGUI serves the web interface and, unless told not to, opens it.
//
// It binds 127.0.0.1 and nothing else. The API can launch processes, rewrite
// launcher.config.json and disable input devices, so exposing it on a LAN
// address would hand those to anything on the network; there is deliberately no
// flag to change the bind address, because "make this reachable from my phone"
// and "make this reachable from everything on the wifi" are the same change and
// only one of them is what anyone means.
//
// It takes no config.Config: main dispatches it before the config load so that
// a malformed launcher.config.json cannot block the settings panel that exists
// to repair it, and the server re-reads the file per request in any case.
func RunGUI(args []string, cfgPath, pbPath string) {
	fs := flag.NewFlagSet("gui", flag.ExitOnError)
	port := fs.Int("port", defaultGUIPort, "TCP port to serve on (loopback only)")
	noOpen := fs.Bool("no-open", false, "do not open a browser window")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: motorhome [-config <path>] gui [-port N] [-no-open]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Serves the web interface on 127.0.0.1 and opens it in a browser.")
		fmt.Fprintln(os.Stderr, "Covers rig control, settings, session analysis, live gaps and personal bests.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	if *port < 1 || *port > 65535 {
		fmt.Fprintf(os.Stderr, "gui: -port must be between 1 and 65535, got %d\n", *port)
		os.Exit(1)
	}

	exe, err := os.Executable()
	if err != nil {
		// Without this the analyze and usb panels cannot re-exec. The rest of
		// the interface still works, so warn rather than refusing to start.
		fmt.Fprintf(os.Stderr, "gui: warning: cannot resolve own path (%v) — analysis and device toggles will be unavailable\n", err)
	}

	deps := gui.Deps{
		ConfigPath:        cfgPath,
		PBPath:            pbPath,
		LoadConfig:        func() (config.Config, error) { return config.Load(cfgPath) },
		SaveConfig:        func(c config.Config) error { return config.Save(cfgPath, c) },
		NewProcessManager: launcher.NewProcessManager,
	}
	if exe != "" {
		deps.RunSubcommand = subcommandRunner(exe, cfgPath)
	}
	// The Windows-only halves — shared memory, SetupAPI, the service control
	// manager. On any other OS these stay nil and their panels report 501.
	attachPlatformDeps(&deps)

	srv := gui.New(deps)
	addr := fmt.Sprintf("127.0.0.1:%d", *port)

	ready := func(actual string) {
		url := "http://" + actual + "/"
		fmt.Printf("MotorHome GUI serving at %s\n", url)
		fmt.Println("Press Ctrl-C to stop.")
		if !*noOpen {
			if err := openBrowser(url); err != nil {
				fmt.Fprintf(os.Stderr, "could not open a browser (%v) — open %s yourself\n", err, url)
			}
		}
	}

	if err := srv.ListenAndServe(addr, ready); err != nil {
		fmt.Fprintf(os.Stderr, "gui: %v\n", err)
		os.Exit(1)
	}
}

// subcommandRunner returns a RunSubcommand that re-execs this binary.
//
// -config is threaded through explicitly rather than relying on the child's
// default: the server may have been started with a -config pointing somewhere
// other than next to the exe, and an analyze that quietly used a different
// config would read a different ibtDir and write a different pb.json than the
// interface is showing.
func subcommandRunner(exe, cfgPath string) func(time.Duration, ...string) ([]byte, error) {
	return func(timeout time.Duration, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		full := append([]string{"-config", cfgPath}, args...)
		cmd := exec.CommandContext(ctx, exe, full...)
		// CombinedOutput because the subcommands split their reporting across
		// both streams: analyze writes its JSON to stdout and its one-line
		// failure reason to stderr, and the caller needs whichever it got.
		out, err := cmd.CombinedOutput()
		if ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("%s timed out after %s", args[0], timeout)
		}
		return out, err
	}
}

// openBrowser opens url in the user's default browser.
//
// On Windows this goes through `cmd /c start` rather than rundll32 so that the
// user's actual default handler is used. The empty first argument to start is
// its title parameter — without it, a quoted URL is taken as the window title
// and nothing opens.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
