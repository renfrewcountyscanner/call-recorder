[CmdletBinding()]
param(
    [string]$LogFile = (Join-Path $env:ProgramData 'CallLogger\proscan-uploader.log'),
    [string]$ServiceName = 'CallLoggerProScanUploader',
    [int]$Tail = 50
)

$ErrorActionPreference = 'Stop'

Write-Host "Call Logger ProScan uploader monitor" -ForegroundColor Cyan
Write-Host "Service: $ServiceName"
Write-Host "Log:     $LogFile"

$service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($null -eq $service) {
    Write-Warning "The Windows service is not installed."
} else {
    Write-Host "Status:  $($service.Status)" -ForegroundColor $(if ($service.Status -eq 'Running') { 'Green' } else { 'Yellow' })
}

if (-not (Test-Path -LiteralPath $LogFile -PathType Leaf)) {
    throw "Log file not found: $LogFile"
}

Write-Host "`nFollowing new log entries. Press Ctrl+C to stop.`n" -ForegroundColor DarkGray
Get-Content -LiteralPath $LogFile -Tail ([Math]::Max(1, $Tail)) -Wait
