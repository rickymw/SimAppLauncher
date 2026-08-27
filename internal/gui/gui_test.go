package gui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rickymw/MotorHome/internal/camera"
	"github.com/rickymw/MotorHome/internal/config"
	"github.com/rickymw/MotorHome/internal/launcher"
	"github.com/rickymw/MotorHome/internal/usbdev"
)

/* ── fakes ─────────────────────────────────────────────────────────── */

// fakePM is a ProcessManager with scripted answers, so the control handlers can
// be exercised without launching anything.
type fakePM struct {
	running   map[string]int
	spawned   []string
	killed    []string
	spawnErr  map[string]error
	killErr   map[string]error
	statusErr map[string]error
}

func newFakePM() *fakePM {
	return &fakePM{
		running:   map[string]int{},
		spawnErr:  map[string]error{},
		killErr:   map[string]error{},
		statusErr: map[string]error{},
	}
}

func (f *fakePM) Spawn(app config.App) launcher.SpawnResult {
	name := app.ProcessName
	if name == "" {
		name = app.Name
	}
	if err := f.spawnErr[name]; err != nil {
		return launcher.SpawnResult{Err: err}
	}
	f.spawned = append(f.spawned, name)
	f.running[name] = 1000 + len(f.spawned)
	return launcher.SpawnResult{PID: f.running[name]}
}

func (f *fakePM) IsRunning(name string) (int, bool, error) {
	if err := f.statusErr[name]; err != nil {
		return 0, false, err
	}
	pid, ok := f.running[name]
	return pid, ok, nil
}

func (f *fakePM) Kill(name string) error {
	if err := f.killErr[name]; err != nil {
		return err
	}
	f.killed = append(f.killed, name)
	delete(f.running, name)
	return nil
}

type fakeUSB struct {
	devs    []usbdev.Device
	scanned []usbdev.Scanned
	err     error

	// gotKnown records the device list the handler passed in, which is how the
	// tests check that the config — not a list captured at boot — is what the
	// provider matches against.
	gotKnown []usbdev.Known
}

func (f *fakeUSB) Enumerate(known []usbdev.Known) ([]usbdev.Device, error) {
	f.gotKnown = known
	return f.devs, f.err
}

func (f *fakeUSB) Scan(known []usbdev.Known) ([]usbdev.Scanned, error) {
	f.gotKnown = known
	return f.scanned, f.err
}

type fakeCamera struct {
	results  []camera.ServiceResult
	err      error
	progress []string
}

func (f *fakeCamera) Restart(progress func(string)) ([]camera.ServiceResult, error) {
	for _, p := range f.progress {
		progress(p)
	}
	return f.results, f.err
}

type fakeLive struct{ snap LiveSnapshot }

func (f fakeLive) Snapshot() LiveSnapshot { return f.snap }

// testServer builds a server over a temp directory holding a real config file,
// so the settings round trip goes through config.Load/Save rather than a stub.
func testServer(t *testing.T, mutate func(*Deps)) (*Server, *fakePM, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "launcher.config.json")

	start := config.Config{
		Driver: "Ricky Maw",
		IbtDir: dir,
		Apps: []config.App{
			{Name: "iRacing", Path: `C:\ir.exe`, ProcessName: "iRacingSim64DX11"},
			{Name: "SimHub", Path: `C:\sh.exe`},
		},
	}
	if err := config.Save(cfgPath, start); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	pm := newFakePM()
	deps := Deps{
		ConfigPath:        cfgPath,
		PBPath:            filepath.Join(dir, "pb.json"),
		LoadConfig:        func() (config.Config, error) { return config.Load(cfgPath) },
		SaveConfig:        func(c config.Config) error { return config.Save(cfgPath, c) },
		NewProcessManager: func() launcher.ProcessManager { return pm },
	}
	if mutate != nil {
		mutate(&deps)
	}
	return New(deps), pm, dir
}

// do issues a request through the full handler chain, including guardLocal, so
// every test also proves the guard lets ordinary traffic past.
func do(t *testing.T, s *Server, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.Host = "127.0.0.1:7777"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %s: %v\nbody: %s", w.Header().Get("Content-Type"), err, w.Body.String())
	}
	return v
}

/* ── guardLocal ────────────────────────────────────────────────────── */

func TestIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1:7777", true},
		{"127.0.0.1", true},
		{"localhost:7777", true},
		{"LOCALHOST:7777", true},
		{"[::1]:7777", true},
		{"127.5.5.5:7777", true}, // the whole 127/8 block is loopback
		{"192.168.1.20:7777", false},
		{"evil.example.com:7777", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isLoopbackHost(c.host); got != c.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// A DNS-rebinding attack reaches the server on 127.0.0.1 but carries its own
// domain in Host, which is exactly what this rejects.
func TestGuardRejectsNonLoopbackHost(t *testing.T) {
	s, _, _ := testServer(t, nil)
	r := httptest.NewRequest("GET", "/api/status", nil)
	r.Host = "attacker.example.com"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestGuardRejectsCrossOrigin(t *testing.T) {
	s, _, _ := testServer(t, nil)
	r := httptest.NewRequest("POST", "/api/stop", nil)
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestGuardAllowsSameOrigin(t *testing.T) {
	s, _, _ := testServer(t, nil)
	r := httptest.NewRequest("GET", "/api/status", nil)
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
}

/* ── control ───────────────────────────────────────────────────────── */

func TestStatusReportsRunningAndStopped(t *testing.T) {
	s, pm, _ := testServer(t, nil)
	pm.running["iRacingSim64DX11"] = 4242

	got := decode[controlResponse](t, do(t, s, "GET", "/api/status", ""))

	if got.Total != 2 || got.Running != 1 {
		t.Fatalf("running/total = %d/%d, want 1/2", got.Running, got.Total)
	}
	if got.Apps[0].Outcome != launcher.OutcomeRunning || got.Apps[0].PID != 4242 {
		t.Errorf("iRacing = %+v, want running with pid 4242", got.Apps[0])
	}
	if got.Apps[1].Outcome != launcher.OutcomeStopped {
		t.Errorf("SimHub = %+v, want stopped", got.Apps[1])
	}
}

// The status row must name the app by its display name while checking the
// process name — the two differ for iRacing in the seeded config, and getting
// this backwards would report a running app as stopped.
func TestStatusUsesProcessNameNotDisplayName(t *testing.T) {
	s, pm, _ := testServer(t, nil)
	pm.running["iRacing"] = 1 // the display name, which must NOT match

	got := decode[controlResponse](t, do(t, s, "GET", "/api/status", ""))
	if got.Running != 0 {
		t.Fatalf("running = %d, want 0 — processName should be what is checked", got.Running)
	}
	if got.Apps[0].Process != "iRacingSim64DX11" {
		t.Errorf("process = %q, want iRacingSim64DX11", got.Apps[0].Process)
	}
}

func TestStartLaunchesStoppedApps(t *testing.T) {
	s, pm, _ := testServer(t, nil)

	got := decode[controlResponse](t, do(t, s, "POST", "/api/start", ""))

	if got.Running != 2 {
		t.Fatalf("running = %d, want 2: %+v", got.Running, got.Apps)
	}
	if len(pm.spawned) != 2 {
		t.Errorf("spawned %v, want both apps", pm.spawned)
	}
}

func TestStopReportsProcessThatSurvives(t *testing.T) {
	// Kill succeeds but the process is still there on the re-check — the
	// SimHub-restarts-itself case. The panel must show it as running, not as
	// closed, which is the whole reason handleStop re-reads status.
	s, pm, _ := testServer(t, nil)
	pm.running["iRacingSim64DX11"] = 10
	pm.running["SimHub"] = 11
	// Kill fails for SimHub, so it is never removed from the running map and
	// the re-check finds it still up.
	pm.killErr["SimHub"] = errors.New("access denied")

	got := decode[controlResponse](t, do(t, s, "POST", "/api/stop", ""))

	var simhub launcher.AppResult
	for _, a := range got.Apps {
		if a.Name == "SimHub" {
			simhub = a
		}
	}
	if simhub.Outcome != launcher.OutcomeFailed {
		t.Fatalf("SimHub outcome = %q, want failed", simhub.Outcome)
	}
	if simhub.Err != "access denied" {
		t.Errorf("SimHub error = %q, want the kill failure carried through", simhub.Err)
	}
}

func TestStopClearsRunningApps(t *testing.T) {
	s, pm, _ := testServer(t, nil)
	pm.running["iRacingSim64DX11"] = 10
	pm.running["SimHub"] = 11

	got := decode[controlResponse](t, do(t, s, "POST", "/api/stop", ""))

	if got.Running != 0 {
		t.Fatalf("running = %d, want 0: %+v", got.Running, got.Apps)
	}
}

func TestControlReportsUnreadableConfig(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.LoadConfig = func() (config.Config, error) { return config.Config{}, errors.New("boom") }
	})
	w := do(t, s, "GET", "/api/status", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(decode[errorBody](t, w).Error, "boom") {
		t.Errorf("error = %q, want the underlying reason", w.Body.String())
	}
}

/* ── settings ──────────────────────────────────────────────────────── */

func TestGetConfigServesTheFile(t *testing.T) {
	s, _, _ := testServer(t, nil)
	got := decode[configResponse](t, do(t, s, "GET", "/api/config", ""))

	if got.Config.Driver != "Ricky Maw" {
		t.Errorf("driver = %q", got.Config.Driver)
	}
	if len(got.Config.Apps) != 2 {
		t.Errorf("apps = %d, want 2", len(got.Config.Apps))
	}
	if len(got.WindowStyles) == 0 {
		t.Error("windowStyles is empty — the form has nothing to build its dropdown from")
	}
}

func TestPutConfigSavesAndReloads(t *testing.T) {
	s, _, dir := testServer(t, nil)
	body := `{"driver":"Someone Else","ibtDir":"` + jsonPath(dir) + `","hotkey":"F13",
	          "whisperPath":"","whisperModel":"",
	          "apps":[{"name":"Only","path":"C:\\only.exe","args":"","windowStyle":"Hidden","delayMs":250,"elevate":false,"processName":"only"}]}`

	w := do(t, s, "PUT", "/api/config", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	// Round-trip through the real loader: the point of the panel is that what
	// it saves is what the CLI will read.
	reloaded, err := config.Load(filepath.Join(dir, "launcher.config.json"))
	if err != nil {
		t.Fatalf("config.Load after save: %v", err)
	}
	if reloaded.Driver != "Someone Else" || len(reloaded.Apps) != 1 {
		t.Fatalf("saved config = %+v", reloaded)
	}
	if reloaded.Apps[0].DelayMs != 250 || reloaded.Apps[0].WindowStyle != "Hidden" {
		t.Errorf("app fields not preserved: %+v", reloaded.Apps[0])
	}
}

// The one failure this panel must not have: writing a config that `motorhome
// start` would then refuse to load, locking the user out through the tool.
func TestPutConfigRejectsInvalidAndLeavesFileAlone(t *testing.T) {
	s, _, dir := testServer(t, nil)
	cfgPath := filepath.Join(dir, "launcher.config.json")
	before, _ := os.ReadFile(cfgPath)

	w := do(t, s, "PUT", "/api/config", `{"driver":"x","ibtDir":"","hotkey":"","whisperPath":"","whisperModel":"","apps":[{"name":"","path":"C:\\x.exe","args":"","windowStyle":"","delayMs":0,"elevate":false,"processName":""}]}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if msg := decode[errorBody](t, w).Error; !strings.Contains(msg, "name is required") {
		t.Errorf("error = %q, want the validation reason", msg)
	}
	after, _ := os.ReadFile(cfgPath)
	if string(before) != string(after) {
		t.Error("a rejected config still rewrote the file")
	}
}

func TestPutConfigRejectsNegativeDelay(t *testing.T) {
	s, _, _ := testServer(t, nil)
	w := do(t, s, "PUT", "/api/config", `{"driver":"","ibtDir":"","hotkey":"","whisperPath":"","whisperModel":"","apps":[{"name":"A","path":"C:\\a.exe","args":"","windowStyle":"","delayMs":-5,"elevate":false,"processName":""}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// A typo'd key must come back as an error rather than being dropped on save,
// leaving the user looking at a setting they believe they changed.
//
// Note the limit of this: encoding/json matches keys case-insensitively, so
// "ibtdir" is accepted as "ibtDir" and only a genuinely different name is
// caught. That is the right trade — a case slip means the same setting, a
// different word means a lost one.
func TestPutConfigRejectsUnknownField(t *testing.T) {
	s, _, _ := testServer(t, nil)
	w := do(t, s, "PUT", "/api/config", `{"driver":"x","ibtDirectory":"typo","apps":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if msg := decode[errorBody](t, w).Error; !strings.Contains(msg, "ibtDirectory") {
		t.Errorf("error = %q, want the offending key named", msg)
	}
}

// The case-insensitive match above is worth pinning down, since it is the
// behaviour a reader of DisallowUnknownFields would not assume.
func TestPutConfigAcceptsDifferentlyCasedKey(t *testing.T) {
	s, _, dir := testServer(t, nil)
	w := do(t, s, "PUT", "/api/config", `{"driver":"x","ibtdir":"`+jsonPath(dir)+`","apps":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := decode[configResponse](t, w); got.Config.IbtDir != dir {
		t.Errorf("ibtDir = %q, want %q", got.Config.IbtDir, dir)
	}
}

// main dispatches gui ahead of config.Load so a malformed config cannot block
// the panel that repairs it. That is only worth anything if a save still works
// while the file on disk is unreadable, which is what this covers.
func TestPutConfigRepairsAnUnreadableFile(t *testing.T) {
	s, _, dir := testServer(t, nil)
	cfgPath := filepath.Join(dir, "launcher.config.json")
	if err := os.WriteFile(cfgPath, []byte("{ broken json"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Reading fails, as the panel would find on load.
	if w := do(t, s, "GET", "/api/config", ""); w.Code != http.StatusInternalServerError {
		t.Fatalf("GET status = %d, want 500 on a broken file", w.Code)
	}

	// Writing a good one over it still works.
	w := do(t, s, "PUT", "/api/config", `{"driver":"Recovered","ibtDir":"","hotkey":"","whisperPath":"","whisperModel":"","apps":[]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", w.Code, w.Body.String())
	}

	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config still unreadable after repair: %v", err)
	}
	if reloaded.Driver != "Recovered" {
		t.Errorf("driver = %q, want the repaired value", reloaded.Driver)
	}
}

func TestPutConfigRejectsGarbage(t *testing.T) {
	s, _, _ := testServer(t, nil)
	if w := do(t, s, "PUT", "/api/config", `not json`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

/* ── sessions ──────────────────────────────────────────────────────── */

func TestSessionsListsIbtFilesNewestFirst(t *testing.T) {
	s, _, dir := testServer(t, nil)

	// Written oldest-first, then stamped, so the ordering under test comes from
	// the mtimes rather than from the order they happen to be read in.
	names := []string{"old.ibt", "mid.IBT", "new.ibt"}
	for i, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(time.Duration(i-3) * time.Hour)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	// A non-telemetry file that must not appear.
	_ = os.WriteFile(filepath.Join(dir, "notes.json"), []byte("{}"), 0o644)

	got := decode[sessionsResponse](t, do(t, s, "GET", "/api/sessions", ""))

	if len(got.Sessions) != 3 {
		t.Fatalf("found %d sessions, want 3: %+v", len(got.Sessions), got.Sessions)
	}
	want := []string{"new.ibt", "mid.IBT", "old.ibt"}
	for i, w := range want {
		if got.Sessions[i].Name != w {
			t.Errorf("session[%d] = %q, want %q", i, got.Sessions[i].Name, w)
		}
		if got.Sessions[i].Index != i+1 {
			t.Errorf("session[%d].Index = %d, want %d", i, got.Sessions[i].Index, i+1)
		}
	}
}

func TestSessionsWithoutIbtDir(t *testing.T) {
	s, _, dir := testServer(t, nil)
	cfgPath := filepath.Join(dir, "launcher.config.json")
	if err := config.Save(cfgPath, config.Config{Driver: "x"}); err != nil {
		t.Fatal(err)
	}
	w := do(t, s, "GET", "/api/sessions", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(decode[errorBody](t, w).Error, "ibtDir") {
		t.Errorf("error = %q, want it to name the missing setting", w.Body.String())
	}
}

/* ── analyze argv ──────────────────────────────────────────────────── */

func TestAnalyzeArgs(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"", []string{"analyze", "-json"}},
		{"lap=3", []string{"analyze", "-json", "-lap", "3"}},
		{"lap=pb", []string{"analyze", "-json", "-lap", "pb"}},
		{"lap=PB", []string{"analyze", "-json", "-lap", "PB"}},
		{"file=C:/x/session.ibt", []string{"analyze", "-json", "C:/x/session.ibt"}},
		{"updateMap=true", []string{"analyze", "-json", "-update-map"}},
		{"fuelLaps=12", []string{"analyze", "-json", "-fuel-laps", "12"}},
		{"trace=T3,T4&hz=20", []string{"analyze", "-json", "-trace", "T3,T4", "-hz", "20"}},
		// hz without trace is dropped rather than passed on, because the
		// subcommand rejects it and the user did not ask for a trace.
		{"hz=20", []string{"analyze", "-json"}},
		// The file must come last so it is not eaten as a flag value.
		{"file=s.ibt&lap=2", []string{"analyze", "-json", "-lap", "2", "s.ibt"}},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/api/analyze?"+c.query, nil)
		got, err := analyzeArgs(r)
		if err != nil {
			t.Errorf("analyzeArgs(%q) errored: %v", c.query, err)
			continue
		}
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("analyzeArgs(%q) = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestAnalyzeArgsRejectsBadValues(t *testing.T) {
	bad := []string{"lap=0", "lap=-1", "lap=banana", "fuelLaps=0", "fuelLaps=x",
		"noteLag=-1", "trace=T3&hz=0", "trace=T3&hz=61", "file=-lap"}
	for _, q := range bad {
		r := httptest.NewRequest("GET", "/api/analyze?"+q, nil)
		if _, err := analyzeArgs(r); err == nil {
			t.Errorf("analyzeArgs(%q) accepted a bad value", q)
		}
	}
}

func TestAnalyzeReturnsSubcommandJSON(t *testing.T) {
	var gotArgs []string
	s, _, _ := testServer(t, func(d *Deps) {
		d.RunSubcommand = func(_ time.Duration, args ...string) ([]byte, error) {
			gotArgs = args
			// A stderr warning ahead of the document is normal: analyze warns
			// about a new PB with no track map, an unreadable notes file, and
			// the clipboard. The handler must find the JSON behind it.
			return []byte("warning: no track map\n{\"schema\":\"motorhome.analyze/1.1\",\"file\":\"x.ibt\"}\n"), nil
		}
	})

	w := do(t, s, "GET", "/api/analyze?lap=2", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if strings.Join(gotArgs, " ") != "analyze -json -lap 2" {
		t.Errorf("argv = %v", gotArgs)
	}

	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response is not the JSON document: %v (%s)", err, w.Body.String())
	}
	if doc["schema"] != "motorhome.analyze/1.1" {
		t.Errorf("schema = %v", doc["schema"])
	}
}

// The subcommand's own message is what tells the user what went wrong ("analyze:
// lap 99 not found"). A generic "exit status 1" would send them nowhere.
func TestAnalyzeSurfacesSubcommandMessage(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.RunSubcommand = func(_ time.Duration, _ ...string) ([]byte, error) {
			return []byte("analyze: lap 99 not found\n"), errors.New("exit status 1")
		}
	})

	w := do(t, s, "GET", "/api/analyze?lap=99", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if msg := decode[errorBody](t, w).Error; !strings.Contains(msg, "lap 99 not found") {
		t.Errorf("error = %q, want the subcommand's reason", msg)
	}
}

func TestAnalyzeRejectsNonJSONOutput(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.RunSubcommand = func(_ time.Duration, _ ...string) ([]byte, error) {
			return []byte("{ this is not json"), nil
		}
	})
	w := do(t, s, "GET", "/api/analyze", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
}

func TestExtractJSONDocument(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"bare", `{"a":1}`, `{"a":1}`, true},
		{"leading warning", "warning: x\n{\"a\":1}", `{"a":1}`, true},
		// main.go wraps every analyze run in the clipboard tee, -json included,
		// so this line always trails a successful run.
		{"trailing clipboard note", "{\"a\":1}\n(copied to clipboard)\n", `{"a":1}`, true},
		{"both sides", "warning: y\n{\"a\":1}\n(copied to clipboard)\n", `{"a":1}`, true},
		{"nested braces survive", `{"a":{"b":[1,2]}}` + "\n(copied to clipboard)", `{"a":{"b":[1,2]}}`, true},
		// A warning containing a brace must not take the response down: the
		// document is whichever brace begins something that actually parses.
		{"brace inside a leading warning", "warning: {weird}\n{\"a\":1}", `{"a":1}`, true},
		{"no document", "no document here", "", false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		got, ok := extractJSONDocument([]byte(c.in))
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

/* ── usb ───────────────────────────────────────────────────────────── */

func devices() []usbdev.Device {
	return []usbdev.Device{
		{Known: usbdev.Known{Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint"}, InstanceID: `USB\VID_30B7&PID_1001\X`, State: usbdev.StateEnabled},
		{Known: usbdev.Known{Alias: "handbrake", Name: "MOZA HBP Handbrake"}, State: usbdev.StateAbsent},
	}
}

func TestUSBListMarksAbsentDevicesUnactionable(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) { d.USB = &fakeUSB{devs: devices()} })

	got := decode[usbResponse](t, do(t, s, "GET", "/api/usb", ""))

	if len(got.Devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(got.Devices))
	}
	if !got.Devices[0].Actionable || got.Devices[0].State != "enabled" {
		t.Errorf("pedals = %+v, want an actionable enabled device", got.Devices[0])
	}
	if got.Devices[1].Actionable {
		t.Error("an unplugged device is offered as actionable — the toggle can only fail")
	}
	if got.Devices[1].State != "not connected" {
		t.Errorf("handbrake state = %q", got.Devices[1].State)
	}
}

// The device list must come from the config on every request, not from
// something captured at boot — otherwise a device added through the picker
// would not appear until the server restarted, which is the friction the
// picker exists to remove.
func TestUSBListUsesTheConfiguredDeviceList(t *testing.T) {
	usb := &fakeUSB{devs: devices()}
	s, _, dir := testServer(t, func(d *Deps) { d.USB = usb })

	cfgPath := filepath.Join(dir, "launcher.config.json")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.USBDevices = []config.USBDevice{
		{Alias: "shifter", Name: "SIMAGIC Q1 Shifter", VID: "0x3670", PID: "0x0401"},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	got := decode[usbResponse](t, do(t, s, "GET", "/api/usb", ""))

	if len(usb.gotKnown) != 1 || usb.gotKnown[0].Alias != "shifter" {
		t.Fatalf("provider matched against %+v, want the configured device", usb.gotKnown)
	}
	if usb.gotKnown[0].VID != 0x3670 || usb.gotKnown[0].PID != 0x0401 {
		t.Errorf("hex IDs not parsed: %+v", usb.gotKnown[0])
	}
	if !got.FromConfig {
		t.Error("fromConfig is false with a configured list — the panel cannot say where the list came from")
	}
}

func TestUSBListFallsBackToBuiltInDevices(t *testing.T) {
	usb := &fakeUSB{devs: devices()}
	s, _, _ := testServer(t, func(d *Deps) { d.USB = usb })

	got := decode[usbResponse](t, do(t, s, "GET", "/api/usb", ""))

	// nil is what ResolveKnown turns into the built-in rig; the handler must
	// not invent a list of its own.
	if len(usb.gotKnown) != 0 {
		t.Errorf("gotKnown = %+v, want nil so usbdev falls back to the built-ins", usb.gotKnown)
	}
	if got.FromConfig {
		t.Error("fromConfig is true with no configured devices")
	}
}

func TestUSBScanReportsKnownAndUnknownDevices(t *testing.T) {
	usb := &fakeUSB{scanned: []usbdev.Scanned{
		{InstanceID: `USB\VID_30B7&PID_1001\X`, VID: 0x30B7, PID: 0x1001,
			Desc: "USB Input Device", State: usbdev.StateEnabled,
			Alias: "pedals", Name: "Heusinkveld Sim Pedals Sprint"},
		{InstanceID: `USB\VID_046D&PID_C52B\Y`, VID: 0x046D, PID: 0xC52B,
			Desc: "Logitech Receiver", State: usbdev.StateEnabled},
	}}
	s, _, _ := testServer(t, func(d *Deps) { d.USB = usb })

	got := decode[scanResponse](t, do(t, s, "GET", "/api/usb/scan", ""))

	if len(got.Devices) != 2 {
		t.Fatalf("scanned %d devices, want 2", len(got.Devices))
	}
	if !got.Devices[0].Known || got.Devices[0].Alias != "pedals" {
		t.Errorf("claimed device = %+v, want it marked known", got.Devices[0])
	}
	// The unclaimed one is the whole point: it is what the picker can offer.
	if got.Devices[1].Known {
		t.Errorf("unclaimed device = %+v, want known false", got.Devices[1])
	}
	// The IDs arrive pre-formatted in the form the config stores, so adding a
	// device is a copy rather than a conversion the page has to get right.
	if got.Devices[1].VID != "0x046D" || got.Devices[1].PID != "0xC52B" {
		t.Errorf("vid/pid = %q/%q, want the config's hex-string form", got.Devices[1].VID, got.Devices[1].PID)
	}
	if got.Devices[1].HardwareID != "VID_046D&PID_C52B" {
		t.Errorf("hardwareId = %q, want the Device Manager form", got.Devices[1].HardwareID)
	}
}

func TestUSBScanUnavailableOffWindows(t *testing.T) {
	s, _, _ := testServer(t, nil)
	if w := do(t, s, "GET", "/api/usb/scan", ""); w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

func TestUSBUnavailableOffWindows(t *testing.T) {
	s, _, _ := testServer(t, nil) // no USB provider
	w := do(t, s, "GET", "/api/usb", "")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

func TestUSBSetShellsOutToSubcommand(t *testing.T) {
	var gotArgs []string
	s, _, _ := testServer(t, func(d *Deps) {
		d.USB = &fakeUSB{devs: devices()}
		d.RunSubcommand = func(_ time.Duration, args ...string) ([]byte, error) {
			gotArgs = args
			return []byte("  [+] pedals  ... disabled\n"), nil
		}
	})

	w := do(t, s, "POST", "/api/usb", `{"action":"off","target":"pedals"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if strings.Join(gotArgs, " ") != "usb off pedals" {
		t.Errorf("argv = %v, want the usb subcommand (which owns the elevation)", gotArgs)
	}
	if got := decode[usbResponse](t, w); !strings.Contains(got.Output, "disabled") {
		t.Errorf("output = %q, want the subcommand's own lines", got.Output)
	}
}

func TestUSBSetRejectsBadAction(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.USB = &fakeUSB{devs: devices()}
		d.RunSubcommand = func(_ time.Duration, _ ...string) ([]byte, error) {
			t.Error("a bad action reached the subcommand")
			return nil, nil
		}
	})
	for _, body := range []string{
		`{"action":"explode","target":"pedals"}`,
		`{"action":"off","target":""}`,
		`{"action":"off","target":"-elevated-out"}`,
	} {
		if w := do(t, s, "POST", "/api/usb", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, w.Code)
		}
	}
}

// A failed toggle still gets the refreshed device list back, because a partial
// failure ("first device off, second refused") did change the rig.
func TestUSBSetReturnsDeviceListOnFailure(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.USB = &fakeUSB{devs: devices()}
		d.RunSubcommand = func(_ time.Duration, _ ...string) ([]byte, error) {
			return []byte("  [!] pedals ... FAILED: access denied\n"), errors.New("exit status 1")
		}
	})

	w := do(t, s, "POST", "/api/usb", `{"action":"off","target":"all"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	got := decode[usbResponse](t, w)
	if len(got.Devices) != 2 {
		t.Error("the failure response dropped the device list, leaving the panel stale")
	}
	if !strings.Contains(got.Output, "access denied") {
		t.Errorf("output = %q", got.Output)
	}
}

/* ── camera ────────────────────────────────────────────────────────── */

func TestCameraReportsWhatItRestarted(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.Camera = &fakeCamera{
			progress: []string{"still waiting for the camera to be released…"},
			results: []camera.ServiceResult{
				{Name: "FrameServer", Restarted: true},
				{Name: "FrameServerMonitor", Restarted: false},
			},
		}
	})

	got := decode[cameraResponse](t, do(t, s, "POST", "/api/camera", ""))

	if got.Restarted != 1 || len(got.Services) != 2 {
		t.Fatalf("got %+v, want 1 of 2 restarted", got)
	}
	if len(got.Progress) != 1 {
		t.Errorf("progress = %v, want the wait explained", got.Progress)
	}
}

func TestCameraReportsFailure(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.Camera = &fakeCamera{err: errors.New("access denied")}
	})
	w := do(t, s, "POST", "/api/camera", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestCameraUnavailableOffWindows(t *testing.T) {
	s, _, _ := testServer(t, nil)
	if w := do(t, s, "POST", "/api/camera", ""); w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

/* ── live ──────────────────────────────────────────────────────────── */

func TestLiveSnapshot(t *testing.T) {
	snap := LiveSnapshot{
		Connected: true, Track: "Watkins Glen", Car: "Porsche 718",
		Position: 3, FieldSize: 20, Lap: 7, LapDistPct: 0.42,
		Ahead: &LiveGap{DriverName: "Someone", TimeSeconds: 1.25},
	}
	s, _, _ := testServer(t, func(d *Deps) { d.Live = fakeLive{snap: snap} })

	got := decode[LiveSnapshot](t, do(t, s, "GET", "/api/live", ""))

	if !got.Connected || got.Track != "Watkins Glen" || got.Position != 3 {
		t.Fatalf("snapshot = %+v", got)
	}
	if got.Ahead == nil || got.Ahead.TimeSeconds != 1.25 {
		t.Errorf("ahead = %+v", got.Ahead)
	}
	// Nobody behind must arrive as null, not as a zero-second gap to a
	// nonexistent car — a solo session is not a car alongside.
	if got.Behind != nil {
		t.Errorf("behind = %+v, want null", got.Behind)
	}
}

// The plain reason and the Win32 diagnostic travel in separate fields, so the
// panel can lead with the first and keep the second as small print.
func TestLiveDisconnectedKeepsMessageAndDetailApart(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.Live = fakeLive{snap: LiveSnapshot{
			Connected: false,
			Message:   "iRacing is not running, or you are not on track",
			Detail:    "OpenFileMappingW: The system cannot find the file specified.",
		}}
	})

	got := decode[LiveSnapshot](t, do(t, s, "GET", "/api/live", ""))

	if got.Connected {
		t.Fatal("reported as connected")
	}
	if strings.Contains(got.Message, "OpenFileMappingW") {
		t.Errorf("message = %q, want the plain reason without the Win32 call", got.Message)
	}
	if !strings.Contains(got.Detail, "OpenFileMappingW") {
		t.Errorf("detail = %q, want the diagnostic preserved", got.Detail)
	}
}

func TestLiveUnavailableOffWindows(t *testing.T) {
	s, _, _ := testServer(t, nil)
	if w := do(t, s, "GET", "/api/live", ""); w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}

func TestLiveStreamEmitsEventsAndStops(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) {
		d.Live = fakeLive{snap: LiveSnapshot{Connected: true, Lap: 4}}
	})

	// A short deadline stands in for the browser closing the EventSource: the
	// handler must return when the request context ends rather than reading
	// shared memory forever.
	r := httptest.NewRequest("GET", "/api/live/stream?hz=60", nil)
	r.Host = "127.0.0.1:7777"
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.Handler().ServeHTTP(w, r); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream handler did not return when its request context ended")
	}

	body := w.Body.String()
	if !strings.Contains(body, "data: ") {
		t.Fatalf("no SSE frames emitted: %q", body)
	}
	if !strings.Contains(body, `"lap":4`) {
		t.Errorf("frame did not carry the snapshot: %q", body)
	}
}

func TestLiveStreamRejectsBadHz(t *testing.T) {
	s, _, _ := testServer(t, func(d *Deps) { d.Live = fakeLive{} })
	if w := do(t, s, "GET", "/api/live/stream?hz=banana", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

/* ── static assets ─────────────────────────────────────────────────── */

// The interface is useless if the embed did not pick the files up, and that is
// a silent failure at build time rather than a compile error.
func TestStaticAssetsAreEmbedded(t *testing.T) {
	s, _, _ := testServer(t, nil)
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		w := do(t, s, "GET", path, "")
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, w.Code)
			continue
		}
		if w.Body.Len() == 0 {
			t.Errorf("GET %s served an empty body", path)
		}
	}
}

/* ── helpers ───────────────────────────────────────────────────────── */

// jsonPath escapes a Windows path for embedding in a JSON string literal.
func jsonPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }
