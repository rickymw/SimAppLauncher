//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/rickymw/MotorHome/internal/audio"
	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/iracing"
	"github.com/rickymw/MotorHome/internal/notes"
	"github.com/rickymw/MotorHome/internal/rawinput"
	"github.com/rickymw/MotorHome/internal/trackmap"
)

// Windows API constants
const (
	whKeyboardLL = 13
	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105
	wmInput      = 0x00FF // WM_INPUT — delivered for registered Raw Input devices
	wmQuit       = 0x0012 // WM_QUIT
)

var (
	modUser32               = syscall.NewLazyDLL("user32.dll")
	modKernel32Notes        = syscall.NewLazyDLL("kernel32.dll")
	procSetWindowsHookExW   = modUser32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = modUser32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = modUser32.NewProc("UnhookWindowsHookEx")
	procGetMessageW         = modUser32.NewProc("GetMessageW")
	procTranslateMessage    = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW    = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage     = modUser32.NewProc("PostQuitMessage")
	procPostThreadMessageW  = modUser32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId  = modKernel32Notes.NewProc("GetCurrentThreadId")
)

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
}

type kbdllHookStruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// notesCtx holds shared state between the hook callback and the recording worker.
type notesCtx struct {
	vkCode       uint32
	startCh      chan struct{}
	stopCh       chan struct{}
	shutdown     chan struct{}
	notesDir     string
	ibtDir       string
	trackmapPath string
	whisperPath  string
	whisperModel string
}

var globalCtx *notesCtx

// captureVKCh receives the VK code detected by set-hotkey (keyboard).
// Initialised at declaration so captureKeyProc can safely send even if called early.
var captureVKCh = make(chan uint32, 1)

// captureHIDCh receives the HIDButton detected by set-hotkey (Raw Input).
var captureHIDCh = make(chan rawinput.HIDButton, 1)

var hookCallback uintptr
var captureCallback uintptr

func init() {
	hookCallback = syscall.NewCallback(lowLevelKeyboardProc)
	captureCallback = syscall.NewCallback(captureKeyProc)
}

func captureKeyProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && captureVKCh != nil {
		if uint32(wParam) == wmKeyDown || uint32(wParam) == wmSysKeyDown {
			hs := (*kbdllHookStruct)(unsafe.Pointer(lParam))
			select {
			case captureVKCh <- hs.VkCode:
			default:
			}
			procPostQuitMessage.Call(0)
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return r
}

func lowLevelKeyboardProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && globalCtx != nil {
		hs := (*kbdllHookStruct)(unsafe.Pointer(lParam))
		if hs.VkCode == globalCtx.vkCode {
			switch uint32(wParam) {
			case wmKeyDown, wmSysKeyDown:
				select {
				case globalCtx.startCh <- struct{}{}:
				default:
				}
			case wmKeyUp, wmSysKeyUp:
				select {
				case globalCtx.stopCh <- struct{}{}:
				default:
				}
			}
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return r
}

// RunNotes dispatches notes subcommands or starts the listener.
func RunNotes(args []string, cfg config.Config, notesDir, cfgPath, trackmapPath string) {
	if len(args) > 0 && args[0] == "set-hotkey" {
		runSetHotkey(cfg, cfgPath)
		return
	}
	runNotesListener(cfg, notesDir, trackmapPath)
}

// runNotesListener starts the voice-notes listener. Blocks until Ctrl+C or process kill.
func runNotesListener(cfg config.Config, notesDir, trackmapPath string) {
	if cfg.Hotkey == "" {
		fmt.Fprintln(os.Stderr, "notes: no hotkey configured — run \"motorhome notes set-hotkey\" to configure one")
		os.Exit(1)
	}

	whisperPath, whisperModel, err := resolveWhisperPaths(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "notes: %v\n", err)
		os.Exit(1)
	}

	shutdown := make(chan struct{})
	globalCtx = &notesCtx{
		startCh:      make(chan struct{}, 1),
		stopCh:       make(chan struct{}, 1),
		shutdown:     shutdown,
		notesDir:     notesDir,
		ibtDir:       cfg.IbtDir,
		trackmapPath: trackmapPath,
		whisperPath:  whisperPath,
		whisperModel: whisperModel,
	}

	go recordingWorker(globalCtx)

	// Lock to OS thread — required for Windows hooks and message loop.
	// Must happen before GetCurrentThreadId so we capture the correct thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// PostQuitMessage only works on the calling thread. The signal handler runs
	// on a different goroutine/OS thread, so use PostThreadMessageW with the
	// explicit thread ID of the locked message-loop thread instead.
	loopThreadID, _, _ := procGetCurrentThreadId.Call()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		close(shutdown)
		procPostThreadMessageW.Call(loopThreadID, wmQuit, 0, 0)
	}()

	fmt.Printf("Listening for %s... Ctrl+C to exit\n", cfg.Hotkey)

	if rawinput.IsHIDHotkey(cfg.Hotkey) {
		// HID path: Raw Input API for steering wheel buttons.
		target, ok := rawinput.ParseHIDButton(cfg.Hotkey)
		if !ok {
			fmt.Fprintf(os.Stderr, "notes: invalid HID hotkey %q\n", cfg.Hotkey)
			os.Exit(1)
		}
		if err := rawinput.Register(); err != nil {
			fmt.Fprintf(os.Stderr, "notes: register raw input: %v\n", err)
			os.Exit(1)
		}
		hidState := rawinput.NewState()
		runMessageLoop(func(lParam uintptr) {
			rawinput.HandleButtonEvent(lParam, target, hidState, globalCtx.startCh, globalCtx.stopCh)
		})
	} else {
		// Keyboard path: low-level keyboard hook.
		vk, err := parseVKey(cfg.Hotkey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "notes: invalid hotkey %q: %v\n", cfg.Hotkey, err)
			os.Exit(1)
		}
		globalCtx.vkCode = vk

		hookHandle, _, _ := procSetWindowsHookExW.Call(whKeyboardLL, hookCallback, 0, 0)
		if hookHandle == 0 {
			fmt.Fprintln(os.Stderr, "notes: failed to install keyboard hook (try running as administrator)")
			os.Exit(1)
		}
		defer procUnhookWindowsHookEx.Call(hookHandle)

		runMessageLoop(nil)
	}
}

func recordingWorker(ctx *notesCtx) {
	// sessionPath is resolved on the first note of the session.
	var sessionPath string

	// segments is loaded lazily on the first note once we know the track name.
	// nil means not yet loaded; empty slice means loaded but no map for this track.
	var segments []trackmap.Segment
	var segmentsTrack string // track name the segments were loaded for

	for {
		select {
		case <-ctx.shutdown:
			return
		case <-ctx.startCh:
			// Drain any stale stop signal from a previous press.
			select {
			case <-ctx.stopCh:
			default:
			}

			rec := &audio.Recorder{}
			if err := rec.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "notes: audio start: %v\n", err)
				continue
			}
			fmt.Print("  [recording...] ")

			// Wait for key release or shutdown.
			select {
			case <-ctx.stopCh:
			case <-ctx.shutdown:
				_, _ = rec.Stop()
				return
			}

			// Capture iRacing context immediately at key release,
			// before transcription introduces a multi-second delay.
			live := iracing.ReadLiveData()
			if !live.Connected && live.ErrMsg != "" {
				fmt.Fprintf(os.Stderr, "\nnotes: iRacing: %s\n", live.ErrMsg)
			}

			// Load segment map for this track the first time we see a track name.
			if live.Track != "" && live.Track != segmentsTrack {
				segmentsTrack = live.Track
				segments = nil // reset; will attempt load below
				if tmf, err := trackmap.Load(ctx.trackmapPath); err == nil {
					if tm, ok := tmf[live.Track]; ok {
						segments = tm.Segments
					}
				}
			}

			pcm, err := rec.Stop()
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nnotes: audio stop: %v\n", err)
				continue
			}

			// Resolve session file on first note.
			if sessionPath == "" {
				var ibtFile string
				sessionPath, ibtFile = resolveSessionPath(ctx.notesDir, ctx.ibtDir)
				session := notes.Session{
					IbtFile: ibtFile,
					Track:   live.Track,
					Car:     live.Car,
					Start:   time.Now().UTC(),
					Notes:   []notes.Note{},
				}
				if err := notes.SaveSession(sessionPath, session); err != nil {
					fmt.Fprintf(os.Stderr, "\nnotes: init session: %v\n", err)
					sessionPath = "" // retry on next note
					continue
				}
				fmt.Printf("\nSession: %s\n", filepath.Base(sessionPath))
			}

			text, err := transcribeLocal(pcm, ctx.whisperPath, ctx.whisperModel)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\nnotes: transcribe: %v\n", err)
				continue
			}

			seg := segmentAtPct(segments, live.LapDistPct)
			note := notes.Note{
				Timestamp:   time.Now().UTC(),
				Lap:         live.Lap,
				LapTime:     live.LapTime,
				LastLapTime: live.LastLapTime,
				SessionTime: live.SessionTime,
				TrackPct:    live.LapDistPct,
				Segment:     seg,
				Text:        text,
			}

			if err := notes.AppendNote(sessionPath, note); err != nil {
				fmt.Fprintf(os.Stderr, "\nnotes: save: %v\n", err)
				continue
			}

			loc := fmt.Sprintf("%.1f%%", live.LapDistPct*100)
			if seg != "" {
				loc = seg + " (" + loc + ")"
			}
			fmt.Printf("[note] lap %d | %.1fs | %s | %s\n", note.Lap, note.LapTime, loc, note.Text)
		}
	}
}

// resolveSessionPath determines the notes file path for the current session.
// It scans ibtDir for the most recently modified .ibt (within the last 4 hours)
// and names the file to match. Falls back to a plain timestamp if none is found.
// The notes directory is created if it does not exist.
func resolveSessionPath(notesDir, ibtDir string) (sessionPath, ibtFile string) {
	if err := os.MkdirAll(notesDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "notes: could not create notes dir: %v\n", err)
	}

	if ibtDir != "" {
		if recent := findRecentIbt(ibtDir, 4*time.Hour); recent != "" {
			base := strings.TrimSuffix(filepath.Base(recent), ".ibt")
			return filepath.Join(notesDir, base+".json"), filepath.Base(recent)
		}
	}

	ts := time.Now().Format("2006-01-02 15-04-05")
	return filepath.Join(notesDir, ts+".json"), ""
}

// findRecentIbt returns the path of the most recently modified .ibt file in dir
// that was modified within maxAge. Returns "" if none found.
func findRecentIbt(dir string, maxAge time.Duration) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	cutoff := time.Now().Add(-maxAge)
	var bestPath string
	var bestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".ibt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) && info.ModTime().After(bestTime) {
			bestTime = info.ModTime()
			bestPath = filepath.Join(dir, e.Name())
		}
	}
	return bestPath
}

// transcribeLocal writes PCM to a temp WAV file and runs whisper-cli to transcribe it.
func transcribeLocal(pcm []byte, whisperPath, modelPath string) (string, error) {
	tmp, err := os.CreateTemp("", "motorhome-*.wav")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(audio.BuildWAV(pcm)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("write wav: %w", err)
	}
	tmp.Close()

	cmd := exec.Command(whisperPath,
		"-m", modelPath,
		"-f", tmpName,
		"-nt",         // no timestamps in output
		"--no-prints", // suppress progress output
		"-l", "en",
	)
	out, err := cmd.Output()
	os.Remove(tmpName) // explicit removal after whisper exits and releases the file
	if err != nil {
		return "", fmt.Errorf("whisper-cli: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// resolveWhisperPaths validates and resolves whisperPath and whisperModel from config.
// Relative paths are resolved relative to the binary's directory.
func resolveWhisperPaths(cfg config.Config) (whisperPath, modelPath string, err error) {
	if cfg.WhisperPath == "" {
		return "", "", fmt.Errorf("whisperPath not set in config — download whisper-cli.exe and set the path")
	}
	if cfg.WhisperModel == "" {
		return "", "", fmt.Errorf("whisperModel not set in config — download a .bin model (e.g. ggml-base.en.bin) and set the path")
	}

	exeDir := filepath.Dir(defaultConfigPath())

	whisperPath = cfg.WhisperPath
	if !filepath.IsAbs(whisperPath) {
		whisperPath = filepath.Join(exeDir, whisperPath)
	}
	if _, err := os.Stat(whisperPath); err != nil {
		return "", "", fmt.Errorf("whisper-cli not found at %q", whisperPath)
	}

	modelPath = cfg.WhisperModel
	if !filepath.IsAbs(modelPath) {
		modelPath = filepath.Join(exeDir, modelPath)
	}
	if _, err := os.Stat(modelPath); err != nil {
		return "", "", fmt.Errorf("whisper model not found at %q", modelPath)
	}

	return whisperPath, modelPath, nil
}

// runSetHotkey waits for a single keyboard key or HID button press and saves it as
// the hotkey in config. Whichever input arrives first wins.
func runSetHotkey(cfg config.Config, cfgPath string) {
	fmt.Println("Press the key or button you want to use for voice notes...")

	// Drain any previously buffered values.
	select {
	case <-captureVKCh:
	default:
	}
	select {
	case <-captureHIDCh:
	default:
	}

	// Register for Raw Input so HID joystick/gamepad buttons are detected.
	// Non-fatal — keyboard detection continues even if this fails.
	if err := rawinput.Register(); err != nil {
		fmt.Fprintf(os.Stderr, "set-hotkey: raw input registration failed (%v) — keyboard-only detection active\n", err)
	}

	hidState := rawinput.NewState()
	onRawInput := func(lParam uintptr) {
		if btn, ok := rawinput.CaptureAnyButton(lParam, hidState); ok {
			select {
			case captureHIDCh <- btn:
			default:
			}
			procPostQuitMessage.Call(0)
		}
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hookHandle, _, _ := procSetWindowsHookExW.Call(whKeyboardLL, captureCallback, 0, 0)
	if hookHandle == 0 {
		fmt.Fprintln(os.Stderr, "set-hotkey: failed to install keyboard hook")
		os.Exit(1)
	}
	defer procUnhookWindowsHookEx.Call(hookHandle)

	runMessageLoop(onRawInput)

	// Determine which input fired (HID takes priority if both somehow arrived).
	var hotkey string
	select {
	case btn := <-captureHIDCh:
		hotkey = btn.String()
	default:
		select {
		case vk := <-captureVKCh:
			if vk != 0 {
				hotkey = vkToName(vk)
			}
		default:
		}
	}

	if hotkey == "" {
		fmt.Fprintln(os.Stderr, "set-hotkey: no input detected")
		os.Exit(1)
	}

	cfg.Hotkey = hotkey
	if err := config.Save(cfgPath, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "set-hotkey: failed to save config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Hotkey set to %q and saved to config.\n", hotkey)
}

// runMessageLoop runs a Windows message pump until WM_QUIT or an error.
// onRawInput, if non-nil, is called for each WM_INPUT message before it is dispatched.
func runMessageLoop(onRawInput func(uintptr)) {
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			break
		}
		if m.Message == wmInput && onRawInput != nil {
			onRawInput(m.LParam)
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// namedVKs maps canonical key names (as stored in config) to Windows virtual key codes.
// This is the single source of truth used by both vkToName and parseVKey.
var namedVKs = map[string]uint32{
	"ScrollLock": 0x91,
	"Pause":      0x13,
	"Insert":     0x2D,
	"Home":       0x24,
	"End":        0x23,
	"PageUp":     0x21,
	"PageDown":   0x22,
	"Delete":     0x2E,
}

// vkToName converts a Windows virtual key code to a human-readable name.
func vkToName(vk uint32) string {
	if vk >= 0x70 && vk <= 0x87 {
		return fmt.Sprintf("F%d", vk-0x6F)
	}
	for name, code := range namedVKs {
		if code == vk {
			return name
		}
	}
	return fmt.Sprintf("0x%02X", vk)
}

// segmentAtPct returns the name of the segment that contains pct (0.0–1.0),
// or "" if segs is empty or no segment covers that position.
// Handles the wrap-around case where a segment straddles the S/F line
// (ExitPct < EntryPct).
func segmentAtPct(segs []trackmap.Segment, pct float32) string {
	for _, s := range segs {
		if s.EntryPct <= s.ExitPct {
			if pct >= s.EntryPct && pct < s.ExitPct {
				return s.Name
			}
		} else {
			// Segment wraps around the S/F line (e.g. 0.97–0.03).
			if pct >= s.EntryPct || pct < s.ExitPct {
				return s.Name
			}
		}
	}
	return ""
}

// parseVKey converts a key name string to a Windows virtual key code.
func parseVKey(s string) (uint32, error) {
	upper := strings.ToUpper(s)

	// F1–F24
	if strings.HasPrefix(upper, "F") {
		n, err := strconv.Atoi(s[1:])
		if err == nil && n >= 1 && n <= 24 {
			return uint32(0x6F + n), nil // F1=0x70, F24=0x87
		}
	}

	// Named keys (case-insensitive)
	for name, vk := range namedVKs {
		if strings.ToUpper(name) == upper {
			return vk, nil
		}
	}

	// Hex e.g. "0x91"
	if strings.HasPrefix(upper, "0X") {
		n, err := strconv.ParseUint(s[2:], 16, 32)
		if err == nil {
			return uint32(n), nil
		}
	}

	// Decimal
	n, err := strconv.ParseUint(s, 10, 32)
	if err == nil {
		return uint32(n), nil
	}

	return 0, fmt.Errorf("unrecognised key %q (try F13, ScrollLock, or a hex code like 0x91)", s)
}
