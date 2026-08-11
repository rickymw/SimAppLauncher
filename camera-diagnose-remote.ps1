# camera-diagnose-remote.ps1
#
# READ-ONLY. Changes nothing. Run this ON THE REMOTE MACHINE, inside the RDP
# session, at the moment the camera reports "in use or unavailable".
#
# Goal: identify what is holding the redirected camera, so the fix can be
# targeted instead of rebooting.

Write-Host "==================================================================="
Write-Host " CAMERA DIAGNOSTIC - run on the REMOTE machine while it is stuck"
Write-Host "==================================================================="
Write-Host ("Machine : {0}" -f $env:COMPUTERNAME)
Write-Host ("Session : {0}" -f $env:SESSIONNAME)
Write-Host ("Time    : {0}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'))

# 1. Who currently holds the camera --------------------------------------
# Windows records camera usage per app. LastUsedTimeStop = 0 means "still
# using it right now". This is the single most useful signal.
Write-Host "`n--- 1. Apps currently HOLDING the camera ---"
$roots = @(
  'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\webcam',
  'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\CapabilityAccessManager\ConsentStore\webcam'
)
$found = $false
foreach ($root in $roots) {
  if (-not (Test-Path $root)) { continue }
  Get-ChildItem $root -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
    $p = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
    if ($null -ne $p.LastUsedTimeStart -and $p.LastUsedTimeStop -eq 0) {
      Write-Host ("  IN USE NOW : {0}" -f $_.PSChildName) -ForegroundColor Yellow
      Write-Host ("               (under {0})" -f $root.Split('\')[0])
      $script:found = $true
    }
  }
}
if (-not $found) {
  Write-Host "  Nothing registered as holding it."
  Write-Host "  -> If the camera still says 'in use', the holder is below the"
  Write-Host "     app layer: the frame server or the redirection stack."
}

# 2. Frame Server services ----------------------------------------------
Write-Host "`n--- 2. Camera Frame Server services ---"
foreach ($n in @('FrameServer','FrameServerMonitor')) {
  $s = Get-CimInstance Win32_Service -Filter "Name='$n'" -ErrorAction SilentlyContinue
  if ($s) {
    Write-Host ("  {0,-20} {1,-10} pid={2,-8} start={3} account={4}" -f `
      $s.Name, $s.State, $s.ProcessId, $s.StartMode, $s.StartName)
  } else {
    Write-Host ("  {0,-20} NOT PRESENT on this machine" -f $n)
  }
}

# 3. RDP device-redirection services -------------------------------------
# UmRdpService is the usermode port redirector that carries redirected
# devices (including the camera) into the session.
Write-Host "`n--- 3. RDP redirection services ---"
foreach ($n in @('UmRdpService','TermService','RpcSs')) {
  $s = Get-CimInstance Win32_Service -Filter "Name='$n'" -ErrorAction SilentlyContinue
  if ($s) { Write-Host ("  {0,-20} {1,-10} pid={2}" -f $s.Name, $s.State, $s.ProcessId) }
}

# 4. Camera devices visible in this session ------------------------------
Write-Host "`n--- 4. Camera devices seen in this session ---"
Get-CimInstance Win32_PnPEntity -ErrorAction SilentlyContinue |
  Where-Object { $_.Name -match 'camera|webcam|C920' -or $_.PNPClass -eq 'Camera' } |
  Select-Object Name, Status, PNPClass, DeviceID |
  Format-Table -AutoSize -Wrap

# 5. Anything with the camera pipeline loaded ----------------------------
Write-Host "--- 5. Processes with a camera/media module loaded ---"
$susp = @()
foreach ($p in Get-Process -ErrorAction SilentlyContinue) {
  try {
    foreach ($m in $p.Modules) {
      if ($m.ModuleName -match '^(mfcore|mfreadwrite|frameserver|devenum|ksuser)\.dll$') {
        $susp += [pscustomobject]@{ Process = $p.ProcessName; Pid = $p.Id; Module = $m.ModuleName }
        break
      }
    }
  } catch { }   # access denied on protected processes is expected
}
if ($susp) { $susp | Sort-Object Process | Format-Table -AutoSize }
else { Write-Host "  (none visible - most will be access-denied without admin)" }

Write-Host "`n==================================================================="
Write-Host " WHAT TO DO WITH THIS"
Write-Host "==================================================================="
Write-Host " * Section 1 names an app  -> close that app; it never released."
Write-Host " * Section 1 empty AND FrameServer is Running (section 2)"
Write-Host "     -> the frame server holds a stale handle. This is the known"
Write-Host "        cause, and the fix is to run ON THIS MACHINE:"
Write-Host "          motorhome.exe camera"
Write-Host "        or manually:  sc stop FrameServer   (it demand-starts again)"
Write-Host " * FrameServer NOT PRESENT -> likely a Server SKU without the camera"
Write-Host "     frame server; the holder is elsewhere in the redirection stack"
Write-Host "     and a reboot may genuinely be the only option."
Write-Host ""
Write-Host " NOTE: disconnecting/reconnecting the RDP session does NOT clear this"
Write-Host " (confirmed). FrameServer is machine-wide, not per-session, so the"
Write-Host " stale handle survives the session being torn down and rebuilt."
