# internal/gui

The web interface behind `motorhome gui`: a stdlib-only HTTP server that
exposes the same operations the CLI subcommands do, plus an editor for
`launcher.config.json`.

## Why a browser and not a window

Every Go GUI toolkit brings a dependency tree this repo does not have — fyne
pulls cgo and OpenGL, walk pulls `golang.org/x/sys` — and `go.mod` has an empty
require block that `internal/camera`, `internal/launcher` and `internal/usbdev`
all went out of their way to keep. `net/http` and `go:embed` are already in the
standard library.

It also happens to be the only option that survives being driven over RDP, which
is how this rig is often used.

## Layout

| File | Role |
|---|---|
| `gui.go` | `Deps`, `Server`, routing, the loopback guard |
| `api.go` | JSON response helpers; the one `{"error": …}` shape |
| `control.go` | `/api/status`, `/api/start`, `/api/stop` |
| `settings.go` | `/api/config` GET and PUT |
| `sessions.go` | `/api/sessions` listing, `/api/analyze` proxy |
| `pbview.go` | `/api/pb` list and detail |
| `devices.go` | `/api/usb` list, set and scan; `/api/camera` |
| `live.go` | `/api/live` snapshot, `/api/live/stream` SSE |
| `static/` | `index.html`, `app.js`, `style.css`, embedded via `go:embed` |

## Dependency injection, and why

Reading iRacing shared memory, enumerating USB devices and driving the service
control manager are all Windows-only. Rather than build-tagging this package,
all three arrive through `Deps` as interfaces. The package therefore compiles
and tests on any OS, and `cmd/motorhome/gui_windows.go` is the single place the
Windows halves are wired in.

A nil provider means "not available on this platform" and its endpoints answer
**501**, not 404 — the page needs to tell "this build cannot do that" from "that
route does not exist", so it can grey the panel out instead of showing an error
the user cannot act on.

## The loopback guard

`guardLocal` wraps the whole mux. The API launches processes, rewrites the
config and disables input devices, so:

- **Host must be a loopback literal.** Binding to `127.0.0.1` stops other
  machines connecting, but not DNS rebinding: a page on the open internet can
  resolve its own hostname to `127.0.0.1` and have the user's browser make these
  requests on its behalf. A rebinding attack has to send its own domain in
  `Host`, which this rejects. `localhost` is accepted because that is what the
  browser sends when the user types it.
- **Origin, when present, must match Host.** Covers ordinary cross-site requests
  from another tab.

There is deliberately no flag to change the bind address. "Reachable from my
phone" and "reachable from everything on the wifi" are the same change, and only
one of them is ever what someone means.

## Two things run out-of-process

`Deps.RunSubcommand` re-execs the binary. Two operations go through it:

**`analyze`** — the opposite of what `coach` does, and for reasons that only
apply to a server. Every error path in the analyze pipeline ends in
`analyzeDie → os.Exit(1)`; in the CLI that is a clean exit, but here a mistyped
lap number would take the whole interface down mid-session. The pipeline also
writes package-level globals (`analyzeOut`, `invokedAs`) and swaps `os.Stdout`,
none of which survives two requests arriving at once. Drift is not a risk
despite the separate path, because it is not a separate path — it is this same
executable running the same subcommand.

`extractJSONDocument` pulls the document out of the child's combined output,
which has prose on both sides: warnings analyze wrote to stderr ahead of it, and
`(copied to clipboard)` behind it, because `main.go` wraps every analyze run in
the clipboard tee regardless of `-json`. It decodes exactly one JSON value and
slices at the decoder's offset, trying each `{` in turn so a warning containing a
brace does not take the response down.

**`usb on|off|toggle`** — changing a device state needs a full administrator
token this process does not have. The `usb` subcommand already knows how to
re-exec itself under UAC and read the elevated child's output back, so the
browser path and the Stream Deck path elevate the same way and there is only one
place where that has to be right. Enumeration stays in-process; it needs no
rights.

## Serialised work

`analyzeMu` serialises analysis runs — each re-reads a whole `.ibt` and may
rewrite `trackmap.json` and `pb.json`, and two racing would interleave those
writes. `cameraMu` does the same for the camera restart, which stops and starts
machine-wide services. Both queue rather than reject: a queued click is less
surprising than a refused one.

## The config is re-read every request

`Deps.LoadConfig` is a function, not a value. The settings panel can rewrite the
file mid-session, and a start issued after an edit must use the edited app list.

`PUT /api/config` replaces the document wholesale (the page always holds the
complete thing it is editing) and runs `config.Validate` before writing — the
same check `motorhome start` runs on load. Saving a config the CLI would then
refuse to read is the one failure this panel must not have: the user would have
locked themselves out of the tool through the tool.

Note that `encoding/json` matches keys case-insensitively, so
`DisallowUnknownFields` catches `ibtDirectory` but not `ibtdir`. That is the
right trade — a case slip means the same setting, a different word means a lost
one.

## Live streaming

`/api/live/stream` is server-sent events rather than a WebSocket: the data only
flows one way, `net/http` speaks SSE with no framing code, and the browser's
`EventSource` reconnects on its own. The request context is the only stop
signal, and the page closes the stream when the user leaves the panel — the one
panel that costs something while hidden.

`LiveSnapshot` splits `Message` from `Detail`. The `live` subcommand prints the
Win32 diagnostic as its whole message, which is right for a command whose `-raw`
mode exists to troubleshoot exactly this; a panel glanced at mid-session wants
"iRacing is not running" with `OpenFileMappingW: The system cannot find the file
specified` as small print underneath.

The gap and position maths is **not** reimplemented here. `gui_windows.go` calls
`gapsFromLive`, the helper `live.go` already uses, which encodes decisions that
are not obvious (shortest on-track distance rather than race position; the
`EstTime` fallback when two cars straddle the S/F line). A second implementation
would eventually disagree with the terminal about the same moment.

## The front end

No framework and no build step — anything needing compilation would mean a
toolchain the repo does not otherwise have. Every panel is the same shape: fetch
JSON, build DOM nodes, replace a container's contents.

Nothing uses `innerHTML` with server data. Track names, driver names and config
paths are free text from iRacing and from the user's own files; building through
`createElement`/`textContent` means a track called `<script>` is a track called
`<script>` rather than a bug.

## The mark

`static/logo.svg` (topbar) and `static/favicon.svg` (tab icon): a slick tyre
under a chevron roofline — a shift light at a glance and a motor *home* on the
second look. Two elements, two colours, drawn on a 64-unit grid, so it survives
down to a 16px favicon.

They are **two files rather than one referenced twice**, which is the only
non-obvious part. The app chrome is dark by deliberate choice (see the note at
the top of `style.css`), so the topbar mark can hard-code a light tyre. A
browser tab strip is not the app and follows the OS, so `favicon.svg` carries a
`prefers-color-scheme` block and swaps the tyre to dark on a light strip. A
single file cannot do both: the media-query version renders a dark tyre on the
dark topbar whenever the OS is in light mode, which is invisible. The accent
roof is the same `#4fa3ff` in both because it reads on either ground.

Note also that the roof and the tyre are drawn with a deliberate gap between
them, sized so it stays open at 16px rather than closing into a single blob.
Adjusting either the roof's `stroke-width` or the circle's radius eats into that
gap from both sides — the clear space is what is left after both strokes, not
the distance between the two paths' centrelines.

`TestBrandingAssets` asserts both files are served as `image/svg+xml` and that
`index.html` still references the names they are served under, because a rename
degrades silently: the page renders perfectly, just with no mark and no tab
icon.

## Testing

`gui_test.go` drives the full handler chain through `httptest`, including
`guardLocal`, with fakes for the process manager and all three platform
providers. The rebinding and cross-origin rejections are covered directly, as is
the settings panel's refusal to write an invalid config, and the SSE handler
returning when its request context ends.

`cmd/motorhome/gui_test.go` covers `subcommandRunner` by re-execing the test
binary through the `TestMain` dispatch `pb_exit_test.go` established.

## The USB picker

`GET /api/usb/scan` returns every USB device on the machine, hubs excluded and
grouped by hardware ID (see [internal/usbdev/README.md](../usbdev/README.md)).
It exists because moving the device list into the config only helps if finding a
VID/PID stops being the hard part — a settings form you type `0x30B7` into is
barely better than a Go file you type it into.

Adding a device from the picker is a config write, not a device operation: the
page reads `/api/config`, appends the entry, and PUTs it back through the same
validation every other settings save goes through. When the config names no
devices yet, the page seeds the built-in list alongside the new entry — without
that, the first Add would silently drop the four defaults, since a configured
list replaces them rather than extending them.

`USBProvider` takes the known-device list per call rather than holding one,
because a controller built once at boot would keep matching against whatever the
server started with, and a device added through the picker would not appear until
a restart — exactly the friction the picker removes.
