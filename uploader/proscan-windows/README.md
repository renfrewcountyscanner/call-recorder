# ProScan Windows uploader

`proscan-uploader.exe` watches one or more ProScan recording directories and sends completed MP3 calls to Call Logger's modern two-stage ingestion API. ProScan does not need to understand the logger credential. The uploader uses its own sender ID and API key.

## Completion and durability guarantees

- Windows directory notifications provide low-latency discovery, with a full periodic rescan as a recovery path.
- Before reading a recording, the uploader opens it through native Windows `CreateFile` with no sharing. `ERROR_SHARING_VIOLATION` and `ERROR_LOCK_VIOLATION` mean ProScan is still writing, so the file remains pending.
- Size and modification time must also remain unchanged for `settle_seconds`.
- A completed source file is copied into a private durable spool before any network request.
- Metadata and audio uploads retry with bounded exponential backoff. Server idempotency makes a retry after an interrupted response safe.
- Successfully processed source fingerprints are retained across restarts. Original recordings are never renamed, changed, or deleted.
- Unknown YAML settings fail validation instead of being silently ignored.

## ProScan metadata

The parser reads ID3v2.3 frames and ProScan's embedded metadata block. It preserves the embedded recording range, scanner, system, department, channel, frequency, modulation, tone, TGID, radio ID when present, RSSI, and digital/DMR fields. Embedded timestamps are interpreted in the configured timezone and sent to the logger in UTC.

The watch-directory `system_id` is authoritative. The scanner model becomes the receiver unless `receiver_id` overrides it. The embedded ProScan system may become the logger site. Conventional recordings without TGIDs receive a stable ID made from frequency and tone, such as `CONV-150.4700-CTCSS-173.8HZ`.

`use_tpe2_radio_id` controls the additional ProScan TPE2 field observed as a radio UID on digital samples. Disable it for scanner profiles that use that field differently.

## Renfrew configuration

[`config.renfrew.yaml`](config.renfrew.yaml) is preconfigured with:

- `E:\BCD996` → logger system `SCANNER-DIGITAL`, receiver `BCD996P2`
- `E:\BCT15X` → logger system `SCANNER-ANALOG`, receiver `BCT15X`

It includes recordings already present when the service first starts. Set `include_existing: false` before installation to upload only future recordings.

## Installation

1. Create a dedicated enabled sender in Call Logger and retain the displayed key.
2. Put the Windows executable, `config.renfrew.yaml`, and `install.ps1` in one folder.
3. Open PowerShell as Administrator.
4. Run:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1
```

The installer prompts for the sender key, stores it outside YAML, restricts the installation to LocalSystem and administrators, validates both recording directories and the logger health endpoint, and installs an automatically starting service with restart-on-failure behavior.

The installer preserves an existing configuration on upgrade. The uninstaller removes only the service; it retains credentials, logs, configuration, and queued recordings.

## Commands

```powershell
# Validate configuration, newest recordings, directories, key, and server health
proscan-uploader.exe check --config C:\ProgramData\CallLogger\proscan-uploader.yaml

# Run interactively for troubleshooting
proscan-uploader.exe run --config C:\ProgramData\CallLogger\proscan-uploader.yaml

# Inspect recordings without uploading
proscan-uploader.exe inspect --directory E:\BCD996 --system-id SCANNER-DIGITAL

# Service controls (elevated PowerShell)
proscan-uploader.exe service --config C:\ProgramData\CallLogger\proscan-uploader.yaml status
proscan-uploader.exe service --config C:\ProgramData\CallLogger\proscan-uploader.yaml restart
```

The default log is `C:\ProgramData\CallLogger\proscan-uploader.log`; pending audio and manifests are under `C:\ProgramData\CallLogger\spool`. Never place the spool inside a watched ProScan directory.
