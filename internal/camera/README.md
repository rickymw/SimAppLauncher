# internal/camera

Restarts a stuck/frozen webcam by restarting the Windows Camera Frame Server.

## What it does

Implements the `camera` subcommand. Stops (if running) and restarts the `FrameServer` and `FrameServerMonitor` Windows services — the shared pipeline every app (Zoom, OBS, Teams, the Camera app, etc.) goes through to access a webcam. This clears the most common "camera frozen / stuck in a bad state" failure without touching the USB device itself.

## Motivating case: Remote Desktop never releases the camera

The webcam is redirected into a Remote Desktop session (`mstsc.exe`). On leaving the meeting, RDP does **not** release the device — it keeps holding the camera indefinitely, so every local app then finds it busy. Nothing will release it on its own, and short of rebooting or unplugging the camera there is no built-in way to recover.

Restarting the Frame Server tears down that stale client handle and frees the device. Confirmed in practice: with `mstsc.exe` holding the camera, running `motorhome camera` released it (the `ConsentStore` in-use entry cleared).

This is also why the graceful-stop wait must be generous — see [Runtime and timeouts](#runtime-and-timeouts). There is no client that will ever voluntarily let go, so Windows waits for a release that never comes and eventually forces it.

### Client side vs host side

Redirection has two ends, each with its own Frame Server, and this command only clears the machine it runs on:

| Symptom | Stuck end | Where to run `camera` |
|---|---|---|
| Local apps say the camera is busy after leaving a meeting | Client (camera physically attached) | The client |
| Apps **inside** the RDP session say "in use or unavailable" | Host (session machine) | The remote machine |

Running it on the client cannot clear a host-side stall — that is what makes the far end look like it needs a reboot. Copy the exe over and run it there; it needs no config file (see below) and no other repo files.

If the remote machine has no `FrameServer` service at all (some Server SKUs), the holder is the redirection stack rather than the frame server, and disconnecting/reconnecting the RDP session is the realistic fix short of a reboot.

## Why not disable/enable the USB PnP device?

That was the first approach tried, using PowerShell's `Disable-PnpDevice`/`Enable-PnpDevice` (and `pnputil`) against the camera's PnP entries. Both failed on this machine even though it can already run `taskkill` against elevated processes via the `SeDebugPrivilege` fallback in [internal/launcher](../launcher/README.md) — disabling/enabling a PnP device needs a genuine administrator token, not just a single grantable privilege, and `ShellExecuteExW`'s `runas` verb doesn't reliably elevate in this environment (see the top-level CLAUDE.md). Restarting the Frame Server services instead only needs `SERVICE_START`/`SERVICE_STOP` rights on those two specific services, which — like `SeDebugPrivilege` — can be granted directly to a non-admin account.

## One-time setup (run once, elevated)

By default only `SYSTEM` and `Administrators` can start/stop these services. Grant your account rights on just these two services (do **not** run this as part of `motorhome camera` — it edits a security descriptor and must be run by hand, once, from an elevated PowerShell):

```powershell
# Look up your account SID:
([System.Security.Principal.WindowsIdentity]::GetCurrent()).User.Value

# Then, substituting that SID, run for both services:
sc.exe sdset FrameServer "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)(A;;RPWPLC;;;<YOUR-SID>)"
sc.exe sdset FrameServerMonitor "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)(A;;RPWPLC;;;<YOUR-SID>)"
```

This appends one ACE granting the account `RP` (start), `WP` (stop), `LC` (query status) on top of the existing default ACL — it does not remove or weaken anything else. Verify with `sc.exe sdshow FrameServer`.

Without this grant, `motorhome camera` fails with an `OpenServiceW` / access-denied error.

## How it works

### Restarter interface

```go
type ServiceResult struct {
    Name      string
    Restarted bool // false = was already stopped, so it was left alone
}

type Restarter interface {
    Restart(progress func(string)) ([]ServiceResult, error)
}
```

`progress` is called with status lines during slow operations, so a wait that legitimately takes ~30s doesn't look like a hang.

`RunCameraRestart(r Restarter)` prints per-service progress and a summary. Tests inject a `mockRestarter` so no real service is touched.

### Runs without a config file

`camera` is the one subcommand dispatched in `main.go` *before* `config.Load`. It reads nothing from the config, and it needs to run from a bare copy of `motorhome.exe` on a machine that has no `launcher.config.json` — the remote end of an RDP session. Loading the config first made that fail with a misleading `error loading config`. `RunCamera` takes no `config.Config` parameter so the independence is explicit rather than incidental.

```powershell
# works with nothing but the exe present
.\motorhome.exe camera
```

### Windows implementation (`camera_windows.go`)

Raw `advapi32.dll` Service Control Manager calls (no external dependencies, matching the rest of the codebase's Windows API style): `OpenSCManagerW` → `OpenServiceW` (`SERVICE_QUERY_STATUS|SERVICE_START|SERVICE_STOP`) → `ControlService(SERVICE_CONTROL_STOP)` and poll `QueryServiceStatus` until `SERVICE_STOPPED` → `StartServiceW` → poll until `SERVICE_RUNNING`. Applied in turn to `FrameServer` then `FrameServerMonitor` (10s timeout per stop/start wait).

**A service that is already stopped is left alone**, not started. Both are `DEMAND_START`, so Windows launches them when an app next opens the camera; starting them here would leave services running that were meant to be idle, and there is no stuck pipeline state to clear when nothing is running. The command therefore restores the original service state rather than unconditionally leaving both running.

### Runtime and timeouts

Runtime depends entirely on whether an app is holding the camera:

| State | Stop time | End to end |
|---|---|---|
| Services stopped (camera idle) | n/a — no-op | ~0.01s |
| Services running, camera not in use | 0.02–0.06s | ~0.48s |
| Camera held by RDP | 0.9s – **30.8s** | 1.0s – **31s** |

Windows will not stop `FrameServer` while a client holds the device — it waits for a graceful release, then forces it. With the camera held by `mstsc.exe` this measured **30.8s** on one run and **1.0s** on another, so the wait is highly variable and the worst case must be tolerated rather than assumed away.

This drove two constants:

- `stopTimeout` is **90s**. It was originally 10s, which meant a restart while the camera was in use hit the timeout and was reported as a *failure* even though the restart was merely slow and would have succeeded.
- `slowStopNotice` is **2s** — after that, `progress` explains that the command is waiting on the app holding the camera, so a legitimate 30s wait doesn't look like a hang.

`pollInterval` is 15ms; at 200ms the four status waits rounded up to ~0.5s of dead time, most of the idle-case runtime.

To find what is holding the camera, check for a subkey with `LastUsedTimeStop = 0` under:

```
HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\webcam
HKLM:\...\ConsentStore\webcam   (for service/system callers)
```

If the console window outlives the times above, that is the launcher rather than this command — check the terminal profile's "close on exit" setting, or whether the Stream Deck action wraps the exe in `cmd /k` instead of invoking it directly.

### Why not just kill the host process?

Terminating the hosting `svchost.exe` would be near-instant instead of waiting up to ~30s, and it would be surgical: each service is alone in its own svchost group (`Camera` → `FrameServer`, `CameraMonitor` → `FrameServerMonitor`, per `HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Svchost`), so no unrelated service would be caught in the blast. Both are `DEMAND_START`, so Windows would bring them back on next camera access.

It is not used because `FrameServer` runs as `NT AUTHORITY\LocalService` and `FrameServerMonitor` as `LocalSystem`; opening them with `PROCESS_TERMINATE` from this non-elevated process is expected to fail with access denied, the same wall `pnputil` hit. (Not directly verified — the service had already stopped when the check was attempted.) The graceful stop works, so the fast path is unnecessary.

## Known limitations

- Restarts the camera pipeline system-wide — if multiple cameras are in use, all of them are affected, not just one.
- Won't fix a genuine USB-level hardware hang (device stopped responding on the bus); only a full PnP disable/enable or physical unplug/replug can, and that needs admin rights this tool doesn't have.

## Architecture

| File | Contents |
|---|---|
| `camera.go` | `Restarter` interface, `RunCameraRestart` |
| `camera_windows.go` | SCM-backed `Restarter` (`OpenSCManagerW` / `OpenServiceW` / `ControlService` / `StartServiceW`) |
