<#
.SYNOPSIS
  Poll a device's MPC measurements and print one line per sample.

.EXAMPLE
  .\mpc-watch.ps1 -Ski 0123456789abcdef0123456789abcdef01234567 -IntervalSeconds 5

.NOTES
  All-null values mean the device is not currently reporting measurements, usually because no
  charging session is active. That is the device's answer, not a script error.
  Ctrl-C to stop.
#>
param(
  [Parameter(Mandatory = $true)][string]$Ski,
  [int]$IntervalSeconds = 5,
  [string]$BaseUrl = "http://127.0.0.1:8080"
)

$uri = "$BaseUrl/api/v1/mpc/$Ski"
"{0,-10} {1,-11} {2,-9} {3}" -f "time", "power_w", "energy_Wh", "current_per_phase_mA"

while ($true) {
  try {
    $d = Invoke-RestMethod -Uri $uri -TimeoutSec 10
    $cur = if ($null -ne $d.current_per_phase_a) { ($d.current_per_phase_a -join ",") } else { "null" }
    "{0,-10} {1,-11} {2,-9} {3}" -f (Get-Date -Format "HH:mm:ss"),
      "$($d.power_w)", "$($d.energy_consumed_wh)", $cur
  }
  catch {
    "{0,-10} request failed: {1}" -f (Get-Date -Format "HH:mm:ss"), $_.Exception.Message
  }
  Start-Sleep -Seconds $IntervalSeconds
}
