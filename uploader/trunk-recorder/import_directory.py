#!/usr/bin/env python3
"""Recursively import legacy logger audio, deleting only successful files."""
import argparse, json, logging, os, re, time
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

def infer(audio, root, cfg):
    parts = audio.relative_to(root).parts; stem = audio.stem
    system = cfg.get("SYSTEM_NAME", "") or (parts[0] if len(parts) > 1 else "")
    receiver = cfg.get("RECEIVER_ID", "") or system; site = cfg.get("SITE_NAME", ""); talkgroup = ""; stamp = None
    match = re.match(r"^(\d{4}-\d{2}-\d{2}_\d{6})_(.+?)_[-_]([^_]*)_(.+?)_(.+)$", stem)
    if match: stamp, talkgroup, site = parse_time(match.group(1)), match.group(2).strip(), site or match.group(4).strip()
    match = re.match(r"^(\d{2}-\d{2}-\d{2} \d{2}-\d{2}-\d{2})\s+-\s+(.+?)\s+-\s+(.+)$", stem)
    if match: stamp, site, talkgroup = parse_time(match.group(1)), site or match.group(2).strip(), match.group(3).strip()
    match = re.match(r"^(.+?)_?(\d{8}_\d{2}-\d{2}-\d{2})$", stem)
    if match: system, receiver, stamp = cfg.get("SYSTEM_NAME", "") or match.group(1).strip(" _-"), receiver, parse_time(match.group(2))
    talkgroup = talkgroup or cfg.get("TALKGROUP_ID", "")
    if not system: raise ValueError(f"cannot infer system for {audio}; set SYSTEM_NAME or use a system subdirectory")
    return {"start_time": (stamp or datetime.now(timezone.utc)).timestamp(), "talkgroup": talkgroup, "talkgroup_description": talkgroup, "talkgroup_tag": talkgroup, "site": site, "site_description": site, "system": system, "receiver": receiver, "call_length": 0}

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
    metadata = {"apiAuthID": cfg["UPLOAD_ID"], "apiKey": cfg["UPLOAD_KEY"], "callAudioFormat": audio.suffix.lstrip(".").lower(), "recordedCall": {"talkGroupInfo": info, "startTime": started, "callDuration": call.get("call_length", 0), "startPositionSec": "00:00:00"}}
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
