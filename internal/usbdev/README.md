# internal/usbdev

Identifies the sim-racing USB devices on this machine and enables or disables
them individually. Backs the `motorhome usb` subcommand.

## Why

Every device on the rig — wheelbase, pedals, handbrake, haptic unit — presents
to Windows as a HID game controller. A game that enumerates all controllers and
auto-binds axes picks up phantom input from the ones that aren't being used:
harmless in a sim, which expects several devices, and disruptive in anything
else. Disabling a device removes its HID node entirely, so nothing can bind to
it until it's turned back on.

## Devices are matched by VID/PID, not by name

Windows reports the friendly name of all four as the generic **"USB Input
Device"**. The real product string lives in `DEVPKEY_Device_BusReportedDeviceDesc`,
which the device itself supplies and which the PnP enumerator does not key on.
So `KnownDevices` carries a curated name per vendor/product ID pair:

| Alias | Device | VID | PID |
|---|---|---|---|
| `wheelbase` | SIMAGIC Alpha Series Wheelbase | `0x0483` | `0x0522` |
| `haptic` | SIMAGIC P2000 Haptic | `0x3670` | `0x0902` |
| `pedals` | Heusinkveld Sim Pedals Sprint | `0x30B7` | `0x1001` |
| `handbrake` | MOZA HBP Handbrake | `0x346E` | `0x001F` |

The wheelbase VID belongs to STMicroelectronics rather than SIMAGIC — the base
uses an ST microcontroller and never overrode the default — so the PID is what
actually pins it down.

That table is the **default**, not the list. `usbDevices` in the config supplies
the real one and `ResolveKnown` falls back to the table above when it is empty.
The list is passed into `NewController` rather than read from the package global,
so the answer never depends on process-wide state a test or a second controller
cannot vary.

A configured list **replaces** the defaults rather than extending them. Additive
sounds safer, but it makes it impossible to drop a device you do not own: a rig
with no haptic unit would carry a phantom `not connected` row forever, and that
state is supposed to mean "unplugged right now", not "belongs to somebody else".
The cost is that hand-adding one device drops the rest — so `FormatList` prints
which list it used, and the GUI picker seeds the defaults when it writes the
first entry.

## `Scan` — every device, not just ours

`Enumerate` had always walked every USB device and discarded the ones that did
not match (`if !ok { continue }`). `Scan` is the same walk without the discard,
and it is what turns adding a device from *find the VID/PID in Device Manager
and type it in* into *pick it from a list*. Both go through `walkUSB`, so a
device the picker offers is by construction one the toggler can find.

Two things the raw walk needed before it was readable — 63 nodes on this rig:

- **Hubs are dropped.** 27 of those 63, none of them a sim control, and
  disabling one takes down everything plugged into it. `IsHubServiceName` keys
  on the driver service rather than the description, because "Generic USB Hub"
  is that string only on an English Windows while a service name is a driver
  identifier and is not translated. The name test lives in the cross-platform
  file so it can be tested without a rig.
- **Rows are grouped by VID/PID**, carrying a `Count`. That is what a device-list
  entry actually selects, and this rig reports eight identical LIGHTSPEED
  receivers under one hardware ID — listing each devnode would offer the same
  "device" eight times, and adding any one of them would claim all eight.

63 rows became 28. Note the honest limitation this exposes: `Enumerate` keeps the
last node it saw for a given ID, so `usb off` on a duplicated hardware ID toggles
an arbitrary one of them. The count is disclosed rather than the behaviour fixed,
because no sim device here duplicates and the fix would need a per-instance list.

IDs are stored in the config as hex **strings** (`"0x30B7"`). JSON has no hex
literal, so numbers would mean writing `12471` for `VID_30B7` — unrecognisable
next to the Device Manager entry it was copied from. `ParseHexID` accepts every
form someone might paste: `0x30B7`, `30B7`, `30b7`, `VID_30B7`.

## Interface nodes are rejected

`parseVIDPID` refuses any instance ID carrying `&MI_`. A composite device like
the MOZA handbrake publishes one devnode per USB interface — a serial port
(`COM6`) and a game controller — beneath a single top-level device node:

```
USB\VID_346E&PID_001F\1D003A001557434232393420      <- toggled
  USB\VID_346E&PID_001F&MI_00\...  USB Serial Device (COM6)
  USB\VID_346E&PID_001F&MI_02\...  USB Input Device
```

Toggling the top-level node is what "turn this device off" means, and takes the
serial port with it. Toggling one interface would leave the physical device
half-on in a way nothing in the output could sensibly describe.

## Three states, not two

`StateAbsent` is distinct from `StateDisabled`. A disabled device still has a
devnode and can be re-enabled; an absent one cannot be acted on at all.
`Enumerate` therefore starts from `KnownDevices` and fills in what it finds, so
a device that isn't plugged in is reported as `not connected` rather than
vanishing from the table — the wheelbase's normal state between sessions.

Presence comes from `SetupDiGetClassDevs` with `DIGCF_PRESENT`; enabled versus
disabled from `CM_Get_DevNode_Status` reporting `CM_PROB_DISABLED`.

## `Resolve` refuses to guess

Like `pb show`, a target matching several devices is an error listing them
rather than a pick. Disabling the wrong device mid-session is cheap to undo but
not cheap to *notice*, since the symptom is a control that silently stopped
working. An exact alias always beats a substring, so adding a device whose name
contains another's alias can't make a working command ambiguous.

`all` deliberately returns absent devices too. Filtering them here would make
`usb off all` silently omit the wheelbase, leaving no way to tell "it's off the
rig" from "the tool doesn't know about it"; the command layer reports and skips
them instead.

## Win32 notes (`usbdev_windows.go`)

Raw `setupapi.dll` / `cfgmgr32.dll` calls via `syscall.NewLazyDLL`, matching the
no-external-dependency style of `internal/camera` and `internal/launcher`.
State changes go through `SetupDiSetClassInstallParams` + `SetupDiCallClassInstaller`
with `DIF_PROPERTYCHANGE`, the same mechanism `devcon` uses.

Two things worth knowing before editing:

- **`SetupDiGetClassDevs` will not take a device instance ID as its
  `Enumerator`**, despite the documentation saying it accepts one — it fails
  with `ERROR_INVALID_DATA` (verified against this rig). `openDevice` builds an
  empty set with `SetupDiCreateDeviceInfoList` and opens the single device into
  it with `SetupDiOpenDeviceInfoW`, which is the unambiguous API for "this
  device and nothing else" and rules out acting on a sibling node.
- **Enable applies two scopes, disable applies one.** A device can be disabled
  in the current hardware profile, globally, or both. Disable sets
  `DICS_FLAG_GLOBAL`; enable clears `DICS_FLAG_CONFIGSPECIFIC` *then*
  `DICS_FLAG_GLOBAL`, because clearing only one leaves the device disabled while
  reporting success.

`restartPending` reads `SP_DEVINSTALL_PARAMS.Flags` for `DI_NEEDRESTART`/
`DI_NEEDREBOOT`. It is effectively never set for a USB HID device, but reporting
success for a change that hasn't taken effect yet would send the user hunting
for a fault in the game instead.

## Elevation

Enumeration and scanning need no special rights. **Changing a device state
requires an elevated token** — `SetupDiCallClassInstaller` returns
`ERROR_ACCESS_DENIED` otherwise. This package does not elevate; it surfaces that
error and lets `cmd/motorhome/usb.go` handle the re-exec.

The elevated child is passed `-config` explicitly. It was not, before the device
list moved into the config — harmless while `usb` read nothing from it, but with
the list there a parent started with `-config D:\other.json` would resolve the
target from one file while the child acted from whatever sat next to the exe,
silently toggling a different device than the one named.

## Testing

`Controller` is an interface so the command layer can be tested with no rig
attached. Everything except the Win32 calls — ID parsing, matching, resolution,
sorting, formatting — lives in the platform-neutral `usbdev.go` and is unit
tested there.
