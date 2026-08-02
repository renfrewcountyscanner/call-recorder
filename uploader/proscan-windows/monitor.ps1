[CmdletBinding()]
param(
    [string]$LogFile = (Join-Path $env:ProgramData 'CallLogger\proscan-uploader.log'),
    [string]$ServiceName = 'CallLoggerProScanUploader',
    [string]$SpoolDirectory = (Join-Path $env:ProgramData 'CallLogger\spool'),
    [int]$Tail = 20,
    [int]$RefreshSeconds = 5
)

$ErrorActionPreference = 'Stop'

if ($RefreshSeconds -lt 1) { $RefreshSeconds = 1 }
if ($Tail -lt 1) { $Tail = 1 }

Write-Host "Call Logger ProScan uploader monitor" -ForegroundColor Cyan
Write-Host "Service: $ServiceName"
Write-Host "Log:     $LogFile"
Write-Host "Spool:   $SpoolDirectory"
Write-Host "Refreshing every $RefreshSeconds second(s). Press Ctrl+C to stop." -ForegroundColor DarkGray

while ($true) {
    Clear-Host
    Write-Host "Call Logger ProScan uploader monitor" -ForegroundColor Cyan
    $service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($null -eq $service) {
        Write-Host "Service: NOT INSTALLED" -ForegroundColor Red
    } else {
        $color = if ($service.Status -eq 'Running') { 'Green' } else { 'Yellow' }
        Write-Host "Service: $($service.Status)" -ForegroundColor $color
    }

    $pendingPath = Join-Path $SpoolDirectory 'pending'
    $statePath = Join-Path $SpoolDirectory 'state\processed.jsonl'
    $pending = @(Get-ChildItem -LiteralPath $pendingPath -Directory -ErrorAction SilentlyContinue)
    $pendingBytes = ($pending | Get-ChildItem -File -Recurse -ErrorAction SilentlyContinue |
        Measure-Object -Property Length -Sum).Sum
    if ($null -eq $pendingBytes) { $pendingBytes = 0 }
    $processed = 0
    if (Test-Path -LiteralPath $statePath -PathType Leaf) {
        $processed = (Get-Content -LiteralPath $statePath -ErrorAction SilentlyContinue |
            Measure-Object -Line).Lines
    }
    Write-Host ("Spool:   {0} pending item(s), {1:N0} bytes; {2:N0} processed" -f $pending.Count, $pendingBytes, $processed)

    if (Test-Path -LiteralPath $LogFile -PathType Leaf) {
        $logItem = Get-Item -LiteralPath $LogFile
        Write-Host ("Log:     {0:N0} bytes, updated {1}" -f $logItem.Length, $logItem.LastWriteTime)
        Write-Host "`nRecent log entries:" -ForegroundColor DarkGray
        Get-Content -LiteralPath $LogFile -Tail $Tail
    } else {
        Write-Host "Log:     not created yet" -ForegroundColor Yellow
    }
    Start-Sleep -Seconds $RefreshSeconds
}
