[CmdletBinding()]
param(
    [string]$Executable = (Join-Path $PSScriptRoot 'proscan-uploader-windows-amd64.exe'),
    [string]$Configuration = (Join-Path $PSScriptRoot 'config.renfrew.yaml')
)

$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this installer from an elevated PowerShell window (Run as administrator).'
}
if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) {
    throw "Uploader executable not found: $Executable"
}
if (-not (Test-Path -LiteralPath $Configuration -PathType Leaf)) {
    throw "Configuration template not found: $Configuration"
}

$programDirectory = Join-Path $env:ProgramFiles 'CallLogger'
$dataDirectory = Join-Path $env:ProgramData 'CallLogger'
$installedExecutable = Join-Path $programDirectory 'proscan-uploader.exe'
$installedConfiguration = Join-Path $dataDirectory 'proscan-uploader.yaml'
$keyFile = Join-Path $dataDirectory 'sender.key'

function Invoke-Uploader {
    param([Parameter(ValueFromRemainingArguments = $true)][string[]]$UploaderArguments)
    & $installedExecutable @UploaderArguments
    if ($LASTEXITCODE -ne 0) {
        throw "Uploader command failed with exit code ${LASTEXITCODE}: $($UploaderArguments -join ' ')"
    }
}

New-Item -ItemType Directory -Force -Path $programDirectory, $dataDirectory, (Join-Path $dataDirectory 'spool') | Out-Null
Copy-Item -LiteralPath $Executable -Destination $installedExecutable -Force
if (-not (Test-Path -LiteralPath $installedConfiguration)) {
    Copy-Item -LiteralPath $Configuration -Destination $installedConfiguration
}
if (-not (Test-Path -LiteralPath $keyFile)) {
    $secureKey = Read-Host 'Paste the Call Logger sender API key' -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureKey)
    try {
        $plainKey = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
        [IO.File]::WriteAllText($keyFile, $plainKey + [Environment]::NewLine, [Text.UTF8Encoding]::new($false))
    }
    finally {
        if ($pointer -ne [IntPtr]::Zero) {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        }
        $plainKey = $null
    }
}

& icacls.exe $dataDirectory /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
if ($LASTEXITCODE -ne 0) { throw "Could not secure $dataDirectory" }
& icacls.exe $programDirectory /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)RX' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
if ($LASTEXITCODE -ne 0) { throw "Could not secure $programDirectory" }

Invoke-Uploader -UploaderArguments @('check', '--config', $installedConfiguration)
& $installedExecutable service --config $installedConfiguration stop 2>$null | Out-Null
& $installedExecutable service --config $installedConfiguration uninstall 2>$null | Out-Null
Invoke-Uploader -UploaderArguments @('service', '--config', $installedConfiguration, 'install')
Invoke-Uploader -UploaderArguments @('service', '--config', $installedConfiguration, 'start')
Write-Host "Installed and started Call Logger ProScan Uploader."
Write-Host "Configuration: $installedConfiguration"
Write-Host "Log: $(Join-Path $dataDirectory 'proscan-uploader.log')"
