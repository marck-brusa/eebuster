<#
.SYNOPSIS
  Apply an LPC consumption limit to a device, then read it back.

.EXAMPLE
  .\lpc-set-limit.ps1 -Ski 0123456789abcdef0123456789abcdef01234567 -Watts 4200
  .\lpc-set-limit.ps1 -Ski 0123456789abcdef0123456789abcdef01234567 -Watts 4200 -Duration PT2H
  .\lpc-set-limit.ps1 -Ski 0123456789abcdef0123456789abcdef01234567 -Watts 0 -Release
#>
param(
  [Parameter(Mandatory = $true)][string]$Ski,
  [Parameter(Mandatory = $true)][int]$Watts,
  # ISO-8601, e.g. PT30S or PT2H. Omit for a limit with no expiry.
  [string]$Duration,
  # is_active:false means "no limit in force", which is not the same as a limit of 0 W.
  [switch]$Release,
  [string]$BaseUrl = "http://127.0.0.1:8080"
)

$ErrorActionPreference = "Stop"

$body = @{ value_w = $Watts; is_active = (-not $Release) }
if ($Duration -and -not $Release) { $body["duration"] = $Duration }
$json = $body | ConvertTo-Json -Compress

$uri = "$BaseUrl/api/v1/lpc/$Ski/limit"
Write-Host "PUT $uri"
Write-Host "  $json"
Invoke-RestMethod -Method Put -Uri $uri -ContentType "application/json" -Body $json |
  ConvertTo-Json -Depth 5

# A device may accept a write and still report the old value briefly; pause before reading
# back so its reporting lag is not mistaken for a failed write.
Start-Sleep -Seconds 3
Write-Host "readback:"
Invoke-RestMethod -Uri $uri | ConvertTo-Json -Depth 5
