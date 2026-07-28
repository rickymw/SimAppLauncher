# internal/camera

Restarts a stuck/frozen webcam by restarting the Windows Camera Frame Server.

## What it does

Implements the `camera` subcommand. Stops (if running) and restarts the `FrameServer` and `FrameServerMonitor` Windows services — the shared pipeline every app (Zoom, OBS, Teams, the Camera app, etc.) goes through to access a webcam. This clears the most common "camera frozen / stuck in a bad state" failure without touching the USB device itself.

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
    Restart() ([]ServiceResult, error)
}
```

`RunCameraRestart(r Restarter)` prints per-service progress and a summary. Tests inject a `mockRestarter` so no real service is touched.

### Windows implementation (`camera_windows.go`)

Raw `advapi32.dll` Service Control Manager calls (no external dependencies, matching the rest of the codebase's Windows API style): `OpenSCManagerW` → `OpenServiceW` (`SERVICE_QUERY_STATUS|SERVICE_START|SERVICE_STOP`) → `ControlService(SERVICE_CONTROL_STOP)` and poll `QueryServiceStatus` until `SERVICE_STOPPED` → `StartServiceW` → poll until `SERVICE_RUNNING`. Applied in turn to `FrameServer` then `FrameServerMonitor` (10s timeout per stop/start wait).

**A service that is already stopped is left alone**, not started. Both are `DEMAND_START`, so Windows launches them when an app next opens the camera; starting them here would leave services running that were meant to be idle, and there is no stuck pipeline state to clear when nothing is running. The command therefore restores the original service state rather than unconditionally leaving both running.

### Runtime

Stop/start of these services measures at 20–110ms each, so `pollInterval` is 15ms. It was originally 200ms, which made the four status waits round up to roughly half a second of dead time — most of the command's total runtime. Measured end-to-end: **~0.48s** when the services are running (the real stuck-camera case), **~0.01s** when they are already stopped.

If the console window stays visible noticeably longer than that, it is the launcher, not this command — check the terminal profile's "close on exit" setting, or whether the Stream Deck action wraps the exe in `cmd /k` rather than invoking it directly.

## Known limitations

- Restarts the camera pipeline system-wide — if multiple cameras are in use, all of them are affected, not just one.
- Won't fix a genuine USB-level hardware hang (device stopped responding on the bus); only a full PnP disable/enable or physical unplug/replug can, and that needs admin rights this tool doesn't have.

## Architecture

| File | Contents |
|---|---|
| `camera.go` | `Restarter` interface, `RunCameraRestart` |
| `camera_windows.go` | SCM-backed `Restarter` (`OpenSCManagerW` / `OpenServiceW` / `ControlService` / `StartServiceW`) |
