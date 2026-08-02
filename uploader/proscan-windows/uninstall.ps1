[CmdletBinding()]
param(
    [string]$Executable = (Join-Path $env:ProgramFiles 'CallLogger\proscan-uploader.exe'),
    [string]$Configuration = (Join-Path $env:ProgramData 'CallLogger\proscan-uploader.yaml')
)

$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) {
    throw "Installed uploader not found: $Executable"
}
& $Executable service --config $Configuration stop 2>$null | Out-Null
& $Executable service --config $Configuration uninstall
if ($LASTEXITCODE -ne 0) {
    throw "Service removal failed with exit code $LASTEXITCODE"
}
Write-Host 'The Windows service was removed.'
Write-Host 'Configuration, credentials, logs, and queued recordings were retained in ProgramData\CallLogger.'
