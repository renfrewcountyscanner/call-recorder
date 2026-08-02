#!/usr/bin/env python3
"""Recursively import legacy logger audio, deleting only successful files."""
import argparse, json, logging, os, re, time, wave
from datetime import datetime, timezone
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

AUDIO_TYPES = {".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4"}
LOG = logging.getLogger("call-recorder.importer")

def post(url, data, headers, timeout):
    try:
        with urlopen(Request(url, data=data, headers=headers, method="POST"), timeout=timeout) as response:
            return response.status, response.read()
    except HTTPError as error:
        return error.code, error.read()

def response(status, body, operation):
    try: value = json.loads(body or b"{}")
    except (TypeError, ValueError) as error: raise RuntimeError(f"{operation} returned non-JSON HTTP {status}") from error
    if status < 200 or status >= 300 or int(value.get("Status", 200)) >= 400:
        message = " ".join(str(value.get("StatusMessage") or value.get("error") or "rejected").split())[:240]
        raise RuntimeError(f"{operation} rejected: HTTP {status}: {message}")
    return value

def parse_time(value):
    for fmt in ("%Y-%m-%d_%H%M%S", "%m-%d-%y %H-%M-%S", "%Y%m%d_%H-%M-%S", "%Y%m%d_%H%M%S"):
        try: return datetime.strptime(value, fmt).replace(tzinfo=timezone.utc)
        except ValueError: pass
    return datetime.now(timezone.utc)

def numeric_id(value):
    """The legacy endpoint accepts numeric IDs; retain labels separately."""
    match = re.search(r"\d+(?:\.\d+)?", str(value or ""))
    return match.group(0) if match else "0"

def audio_duration_seconds(audio):
    """Read WAV duration exactly; estimate MP3 duration from its frame bitrate."""
    if audio.suffix.lower() == ".wav":
        with wave.open(str(audio), "rb") as stream:
            return stream.getnframes() / float(stream.getframerate())
    data = audio.read_bytes()[:8192]
    offset = 0
    if data[:3] == b"ID3" and len(data) >= 10:
        offset = 10 + sum((data[index] & 0x7f) << (7 * (9 - index)) for index in range(6, 10))
    for index in range(offset, max(offset, len(data) - 3)):
        header = int.from_bytes(data[index:index + 4], "big")
        if header >> 21 != 0x7ff:
            continue
        version, layer, bitrate_index = (header >> 19) & 3, (header >> 17) & 3, (header >> 12) & 15
        if layer != 1 or bitrate_index in (0, 15):
            continue
        table = (0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320) if version == 3 else (0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160)
        return max(0.001, (audio.stat().st_size - index) * 8 / (table[bitrate_index] * 1000))
    # The importer prefers an approximate but non-zero duration over throwing
    # away an otherwise valid legacy recording with an uncommon codec.
    return max(0.001, audio.stat().st_size * 8 / 64000)

def infer(audio, root, cfg):
    parts = audio.relative_to(root).parts; stem = audio.stem
    system = cfg.get("SYSTEM_NAME", "")
    receiver = cfg.get("RECEIVER_ID", "")
    site = cfg.get("SITE_NAME", ""); talkgroup = ""; talkgroup_label = ""; source = ""; stamp = None; source_label = ""
    match = re.match(r"^(\d{4}-\d{2}-\d{2}_\d{6})_(.+?)_([^_]*)_(.*?)_([^_]*)$", stem)
    if match:
        stamp, talkgroup_label, source, site, source_label = parse_time(match.group(1)), match.group(2).strip(), numeric_id(match.group(3)), site or match.group(4).strip(), match.group(5).strip()
        talkgroup = numeric_id(talkgroup_label)
    match = re.match(r"^(\d{2}-\d{2}-\d{2} \d{2}-\d{2}-\d{2})\s+-\s+(.+?)\s+-\s+(.+)$", stem)
    if match:
        stamp, site, talkgroup_label = parse_time(match.group(1)), site or match.group(2).strip(), match.group(3).strip()
        talkgroup = numeric_id(talkgroup_label)
    match = re.match(r"^(.+?)_?(\d{8}_\d{2}-\d{2}-\d{2})$", stem)
    if match: system, source_label, stamp = system or match.group(1).strip(" _-"), match.group(1).strip(" _-"), parse_time(match.group(2))
    talkgroup = talkgroup or cfg.get("TALKGROUP_ID", "0")
    talkgroup_label = talkgroup_label or talkgroup
    system = system or source_label or (parts[0] if len(parts) > 1 else "")
    receiver = receiver or source_label or system
    if not system: raise ValueError(f"cannot infer system for {audio}; set SYSTEM_NAME or use a system subdirectory")
    return {"start_time": (stamp or datetime.now(timezone.utc)).timestamp(), "talkgroup": talkgroup, "talkgroup_description": talkgroup_label, "talkgroup_tag": talkgroup_label, "source": source, "site": site, "site_description": site, "system": system, "receiver": receiver, "call_length": 0}

def load_call(audio, root, cfg):
    sidecar = audio.with_suffix(".json")
    if not sidecar.is_file(): return infer(audio, root, cfg)
    value = json.loads(sidecar.read_text(encoding="utf-8"))
    if "recordedCall" in value:
        recorded = value["recordedCall"]; info = recorded.get("talkGroupInfo", {}); target = (info.get("callTargets") or [{}])[0]
        return {"start_time": recorded.get("startTime"), "talkgroup": target.get("targetid", ""), "talkgroup_description": target.get("targetlabel", ""), "talkgroup_tag": target.get("targettag", ""), "site": info.get("siteid", ""), "site_description": info.get("sitelabel", ""), "system": info.get("systemid", ""), "receiver": info.get("receiver", ""), "call_length": recorded.get("callDuration", 0)}
    return value

def upload(audio, call, cfg):
    start = call.get("start_time")
    if isinstance(start, str): start = datetime.fromisoformat(start.replace("Z", "+00:00")).timestamp()
    started = datetime.fromtimestamp(float(start), timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")
    system = call.get("system") or cfg.get("SYSTEM_NAME", ""); receiver = call.get("receiver") or cfg.get("RECEIVER_ID") or system
    target = {"targetid": call.get("talkgroup", ""), "targetlabel": call.get("talkgroup_description", ""), "targettag": call.get("talkgroup_tag", "")}
    info = {"callTargets": [target], "receiver": receiver, "frequency": call.get("freq", ""), "sourceid": call.get("source", ""), "sourcelabel": call.get("source_description", ""), "sourcetag": "", "lcn": call.get("lcn", ""), "voiceservice": call.get("voice_service", ""), "systemid": system, "systemlabel": system, "systemtype": "", "siteid": call.get("site", ""), "sitelabel": call.get("site_description", ""), "calltype": "1"}
    duration = float(call.get("call_length") or 0) or audio_duration_seconds(audio)
    metadata = {"apiAuthID": cfg["UPLOAD_ID"], "apiKey": cfg["UPLOAD_KEY"], "imported": True, "callAudioFormat": audio.suffix.lstrip(".").lower(), "recordedCall": {"talkGroupInfo": info, "startTime": started, "callDuration": duration, "startPositionSec": "00:00:00"}}
    base = cfg["DESTINATION_URL"].rstrip("/"); timeout = int(cfg.get("TIMEOUT_SECONDS", "30"))
    status, body = post(base + "/api/callupload", json.dumps(metadata).encode(), {"Content-Type": "application/json"}, timeout); result = response(status, body, "metadata")
    if result.get("Duplicate"): return
    token = result.get("CallAudioID")
    if not token: raise RuntimeError("metadata accepted without CallAudioID")
    status, body = post(base + "/api/callaudioupload/" + token, audio.read_bytes(), {"Content-Type": AUDIO_TYPES[audio.suffix.lower()]}, timeout); response(status, body, "audio")

def scan(root, cfg, min_age):
    imported = failed = 0
    for audio in sorted(p for p in root.rglob("*") if p.is_file() and p.suffix.lower() in AUDIO_TYPES):
        if time.time() - audio.stat().st_mtime < min_age: continue
        try:
            upload(audio, load_call(audio, root, cfg), cfg); sidecar = audio.with_suffix(".json"); audio.unlink()
            if sidecar.is_file(): sidecar.unlink()
            imported += 1; LOG.info("imported %s", audio)
        except (OSError, ValueError, RuntimeError, URLError) as error:
            failed += 1; LOG.error("could not import %s: %s", audio, error)
    for directory in sorted((p for p in root.rglob("*") if p.is_dir()), key=lambda p: len(p.parts), reverse=True):
        try: directory.rmdir()
        except OSError: pass
    return imported, failed

def main():
    parser = argparse.ArgumentParser(); parser.add_argument("--root", default=os.environ.get("IMPORT_ROOT", "/logger-import")); parser.add_argument("--min-age", type=int, default=int(os.environ.get("IMPORT_MIN_AGE_SECONDS", "120"))); args = parser.parse_args()
    cfg = dict(os.environ); missing = [key for key in ("DESTINATION_URL", "UPLOAD_ID", "UPLOAD_KEY") if not cfg.get(key)]
    if missing: parser.error("missing environment: " + ", ".join(missing))
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s"); root = Path(args.root); root.mkdir(parents=True, exist_ok=True)
    imported, failed = scan(root, cfg, args.min_age); LOG.info("scan complete imported=%d failed=%d", imported, failed); return 1 if failed else 0

if __name__ == "__main__": raise SystemExit(main())
