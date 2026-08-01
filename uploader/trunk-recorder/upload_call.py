#!/usr/bin/env python3
"""Durable two-stage sender for completed Trunk Recorder calls (stdlib only)."""
import argparse, json, os, shutil, sys, time, uuid
from datetime import datetime, timezone
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

def envfile(path):
    for line in Path(path).read_text().splitlines():
        if "=" in line and not line.lstrip().startswith("#"):
            k, v = line.split("=", 1); os.environ.setdefault(k.strip(), v.strip())

def _post(url, data, headers, timeout):
    """Return a consistent response tuple, including HTTP error headers."""
    req = Request(url, data=data, headers=headers, method="POST")
    try:
        with urlopen(req, timeout=timeout) as res:
            return res.status, res.headers.get("Content-Type", ""), res.read()
    except HTTPError as err:
        return err.code, err.headers.get("Content-Type", "") if err.headers else "", err.read()

def _json_response(status, content_type, body, operation):
    try:
        response = json.loads(body or b"{}")
    except (TypeError, ValueError):
        raise RuntimeError(f"{operation} response was not JSON (http={status}, content_type={content_type or 'unknown'})")
    if not isinstance(response, dict):
        raise RuntimeError(f"{operation} response was not a JSON object (http={status})")
    return response

def _status_error(response, fallback):
    message = response.get("StatusMessage") or response.get("error") or fallback
    message = " ".join(str(message).split())[:240]
    return message

def legacy_metadata(call, cfg, audio):
    targets = [{"targetid": call.get("talkgroup", ""), "targetlabel": call.get("talkgroup_description", ""), "targettag": call.get("talkgroup_tag", "")}]
    start = datetime.fromtimestamp(float(call["start_time"]), timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")
    return {"apiAuthID":cfg["UPLOAD_ID"], "apiKey":cfg["UPLOAD_KEY"], "callAudioFormat":audio.suffix.lstrip(".").lower(), "recordedCall":{"callText":"", "talkGroupInfo":{"callTargets":targets,"receiver":"Trunk-Recorder " + cfg.get("SYSTEM_NAME", ""),"receiverVCO":0,"frequency":call.get("freq", ""),"sourceid":call.get("source", ""),"sourcelabel":call.get("source_description", ""),"sourcetag":"","lcn":call.get("lcn", ""),"voiceservice":call.get("voice_service", ""),"systemid":cfg.get("SYSTEM_NAME", ""),"systemlabel":"","systemtype":"","siteid":call.get("site", ""),"sitelabel":call.get("site_description", ""),"calltype":"1"},"startTime":start,"callDuration":call.get("call_length", 0),"startPositionSec":"00:00:00"}}

def attempt(item, cfg):
    audio, call = Path(item["audio"]), json.loads(Path(item["metadata"]).read_text())
    payload = json.dumps(legacy_metadata(call, cfg, audio)).encode()
    status, content_type, body = _post(cfg["DESTINATION_URL"].rstrip("/")+"/api/callupload", payload, {"Content-Type":"application/json"}, int(cfg.get("TIMEOUT_SECONDS", "30")))
    response = _json_response(status, content_type, body, "metadata")
    try:
        application_status = int(response.get("Status", 500))
    except (TypeError, ValueError):
        application_status = 500
    if status < 200 or status >= 300 or application_status >= 400:
        raise RuntimeError(f"metadata rejected: http={status}, status={application_status}, message={_status_error(response, 'rejected')}")
    if response.get("Duplicate"):
        return
    token = response.get("CallAudioID")
    if not isinstance(token, str) or not token.strip():
        raise RuntimeError("metadata accepted without CallAudioID")
    mime = "audio/mpeg" if audio.suffix.lower()==".mp3" else "audio/wav"
    status, content_type, body = _post(cfg["DESTINATION_URL"].rstrip("/")+"/api/callaudioupload/"+token, audio.read_bytes(), {"Content-Type":mime}, int(cfg.get("TIMEOUT_SECONDS", "30")))
    response = _json_response(status, content_type, body, "audio")
    try:
        application_status = int(response.get("Status", 500))
    except (TypeError, ValueError):
        application_status = 500
    if status < 200 or status >= 300 or application_status >= 400:
        raise RuntimeError(f"audio rejected: http={status}, status={application_status}, message={_status_error(response, 'rejected')}")

def destinations(cfg):
    result = [cfg]
    if cfg.get("SECONDARY_DESTINATION_URL"):
        secondary = dict(cfg)
        secondary["DESTINATION_URL"] = cfg["SECONDARY_DESTINATION_URL"]
        secondary["UPLOAD_ID"] = cfg["SECONDARY_UPLOAD_ID"]
        secondary["UPLOAD_KEY"] = cfg["SECONDARY_UPLOAD_KEY"]
        result.append(secondary)
    return result

def queue(audio, metadata, cfg):
    root=Path(cfg["SPOOL_DIR"]); pending=root/"pending"; failed=root/"failed"; pending.mkdir(parents=True, exist_ok=True); failed.mkdir(parents=True, exist_ok=True)
    item=pending/(uuid.uuid4().hex+".json"); item.write_text(json.dumps({"audio":str(Path(audio).resolve()),"metadata":str(Path(metadata).resolve()),"attempts":0,"next":0}), encoding="utf-8"); return item

def safe_error(error, cfg):
    message = " ".join(str(error).split())
    for name in ("UPLOAD_KEY", "SECONDARY_UPLOAD_KEY"):
        secret = cfg.get(name, "")
        if secret:
            message = message.replace(secret, "[redacted]")
    return message[:500] or error.__class__.__name__

def drain(cfg):
    root=Path(cfg["SPOOL_DIR"]); pending=root/"pending"; failed=root/"failed"; retries=int(cfg.get("RETRY_COUNT","5"))
    pending.mkdir(parents=True, exist_ok=True); failed.mkdir(parents=True, exist_ok=True)
    for item in sorted(pending.glob("*.json")):
        record=json.loads(item.read_text())
        if record["next"] > time.time(): continue
        try:
            for destination in destinations(cfg):
                try:
                    attempt(record, destination)
                except (OSError, ValueError, URLError, RuntimeError) as error:
                    sender = " ".join(destination.get("UPLOAD_ID", "unknown").split())[:128]
                    raise RuntimeError(f"sender={sender} {safe_error(error, destination)}") from error
            item.unlink()
        except (OSError, ValueError, URLError, RuntimeError) as error:
            record["attempts"] += 1
            if record["attempts"] > retries:
                shutil.move(str(item), failed/item.name)
                state = "failed"
            else:
                record["next"] = time.time()+min(300, 2**record["attempts"]); item.write_text(json.dumps(record))
                state = "pending"
            print(f"call-recorder upload failed item={item.name} attempts={record['attempts']} state={state} error={safe_error(error, cfg)}", file=sys.stderr)
    pending_count = sum(1 for _ in pending.glob("*.json"))
    failed_count = sum(1 for _ in failed.glob("*.json"))
    if pending_count or failed_count:
        print(f"call-recorder spool not empty pending={pending_count} failed={failed_count}", file=sys.stderr)
        return False
    return True

def main():
    p=argparse.ArgumentParser(); p.add_argument("--env", required=True); p.add_argument("--audio"); p.add_argument("--metadata"); p.add_argument("--drain", action="store_true"); a=p.parse_args(); envfile(a.env); cfg=dict(os.environ)
    if a.drain: return 0 if drain(cfg) else 1
    if not a.audio or not a.metadata: p.error("--audio and --metadata are required")
    queue(a.audio,a.metadata,cfg); return 0 if drain(cfg) else 1
if __name__ == "__main__": raise SystemExit(main())
