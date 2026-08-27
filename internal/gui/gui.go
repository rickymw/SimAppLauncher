// Package gui serves the MotorHome web interface: a local, stdlib-only HTTP
// server that exposes the same operations the CLI subcommands do, plus an
// editor for launcher.config.json.
//
// It is a browser UI rather than a native window because that is the only way
// to get a real interface out of this repo without taking on an external
// dependency — every GUI toolkit for Go pulls in either cgo/OpenGL (fyne) or
// golang.org/x/sys (walk), and the whole project has an empty require block.
// net/http and go:embed are already in the standard library. It also happens to
// be the only option that survives being used over RDP, which is how this rig
// is often driven.
//
// Everything platform-specific — reading iRacing shared memory, enumerating USB
// devices, restarting the camera pipeline — arrives through the Deps struct
// rather than being called directly, so this package compiles and tests on any
// OS while the Windows halves stay in the Windows-only files that already own
// them.
package gui

import (
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rickymw/MotorHome/internal/camera"
	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/launcher"
	"github.com/rickymw/MotorHome/internal/usbdev"
)

//go:embed static
var staticFS embed.FS

// USBProvider enumerates the sim-racing USB devices. It is only the read half
// of usbdev.Controller: changing a device state needs an elevated token, which
// this process does not have and must not try to acquire while holding an HTTP
// request open — those go out through RunSubcommand to the `usb` subcommand,
// which already knows how to re-exec itself under UAC.
//
// Both methods take the known-device list per call rather than the provider
// holding one, because the settings panel can now edit that list: a controller
// built once at boot would keep matching against the devices the server started
// with, and a device added through the picker would not appear until a restart.
type USBProvider interface {
	Enumerate(known []usbdev.Known) ([]usbdev.Device, error)
	// Scan reports every USB device on the machine, including unrecognised
	// ones, so the picker can offer them.
	Scan(known []usbdev.Known) ([]usbdev.Scanned, error)
}

// CameraProvider restarts the OS camera pipeline. Mirrors camera.Restarter.
type CameraProvider interface {
	Restart(progress func(string)) ([]camera.ServiceResult, error)
}

// LiveProvider returns a snapshot of the iRacing session. The gap and position
// computation happens on the caller's side of this interface so that this
// package does not have to reach into Windows-only shared-memory code; the
// Windows shim builds the snapshot from the same helpers the `live` subcommand
// uses, so the two views cannot disagree.
type LiveProvider interface {
	Snapshot() LiveSnapshot
}

// Deps is everything the server needs from the outside world. A nil provider
// means "not available on this platform", which the handlers report as a 501
// rather than a failure — the UI greys the panel out instead of showing an
// error the user cannot act on.
type Deps struct {
	// ConfigPath is the launcher.config.json the settings panel reads and
	// writes. PBPath is pb.json.
	ConfigPath string
	PBPath     string

	// LoadConfig re-reads the config from disk. It is a function rather than a
	// value because the settings panel can change it mid-session, and a start
	// issued after an edit must use the edited app list, not the one that was
	// loaded when the server booted.
	LoadConfig func() (config.Config, error)
	SaveConfig func(config.Config) error

	NewProcessManager func() launcher.ProcessManager

	USB    USBProvider
	Camera CameraProvider
	Live   LiveProvider

	// RunSubcommand re-runs this executable with the given arguments and
	// returns its combined output. Used for the two operations that must not
	// run in this process: `analyze`, whose error paths call os.Exit and would
	// take the server down with them, and `usb on|off|toggle`, which re-execs
	// itself elevated.
	RunSubcommand func(timeout time.Duration, args ...string) (out []byte, err error)
}

// Server holds the running interface.
type Server struct {
	deps Deps

	// analyzeMu serialises analysis runs. Each one re-reads a whole .ibt and
	// may rewrite trackmap.json and pb.json; two of them racing would interleave
	// those writes. Requests queue rather than fail — an analysis takes a couple
	// of seconds and a queued click is less surprising than a rejected one.
	analyzeMu sync.Mutex

	// cameraMu does the same for the camera restart, which stops and starts
	// machine-wide services and can block for ~30s.
	cameraMu sync.Mutex
}

func New(deps Deps) *Server { return &Server{deps: deps} }

// Handler returns the mux with the safety middleware already wrapped around it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/start", s.handleStart)
	mux.HandleFunc("POST /api/stop", s.handleStop)

	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config", s.handlePutConfig)

	mux.HandleFunc("GET /api/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/analyze", s.handleAnalyze)

	mux.HandleFunc("GET /api/pb", s.handlePBList)

	mux.HandleFunc("GET /api/usb", s.handleUSBList)
	mux.HandleFunc("POST /api/usb", s.handleUSBSet)
	mux.HandleFunc("GET /api/usb/scan", s.handleUSBScan)
	mux.HandleFunc("POST /api/camera", s.handleCamera)

	mux.HandleFunc("GET /api/live", s.handleLive)
	mux.HandleFunc("GET /api/live/stream", s.handleLiveStream)

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Only reachable if the embed directive and the directory disagree,
		// which is a build-time mistake rather than a runtime condition.
		panic("gui: embedded static assets missing: " + err.Error())
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))

	return guardLocal(mux)
}

// guardLocal rejects anything that did not come from this machine's own browser
// talking to a loopback address.
//
// This matters more than it looks: the API can launch processes, rewrite the
// config and disable input devices. Binding to 127.0.0.1 stops other machines
// connecting, but it does not stop DNS rebinding — a page on the open internet
// can resolve its own hostname to 127.0.0.1 and have the user's browser make
// these requests for it. Checking that the Host header is a loopback literal
// closes that, because a rebinding attack has to send its own domain in Host.
// The Origin check covers ordinary cross-site requests from another tab.
func guardLocal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "motorhome gui only serves loopback addresses", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !originMatchesHost(origin, r.Host) {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether a Host header names a loopback address. A bare
// "localhost" is accepted because that is what the browser sends when the user
// types it, and it cannot be pointed elsewhere by a remote party the way an
// arbitrary hostname can.
func isLoopbackHost(host string) bool {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// ListenAndServe binds addr and serves until the process ends. It reports the
// resolved address through ready before blocking, so a caller asking for port 0
// still learns where to point the browser.
func (s *Server) ListenAndServe(addr string, ready func(string)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", addr, err)
	}
	if ready != nil {
		ready(ln.Addr().String())
	}
	srv := &http.Server{
		Handler: s.Handler(),
		// Only the header timeout is set. An analyze of a long session and a
		// camera restart waiting on a stuck client both legitimately take tens
		// of seconds, and the live stream is open-ended by design, so a write
		// timeout would cut off correct work.
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.Serve(ln)
}
