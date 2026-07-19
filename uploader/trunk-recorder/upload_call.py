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

def post(url, data, headers, timeout):
    req = Request(url, data=data, headers=headers, method="POST")
    try:
        with urlopen(req, timeout=timeout) as res: return res.status, res.read()
    except HTTPError as err: return err.code, err.read()

def legacy_metadata(call, cfg, audio):
    targets = [{"targetid": call.get("talkgroup", ""), "targetlabel": call.get("talkgroup_description", ""), "targettag": call.get("talkgroup_tag", "")}]
    start = datetime.fromtimestamp(float(call["start_time"]), timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z")
    return {"apiAuthID":cfg["UPLOAD_ID"], "apiKey":cfg["UPLOAD_KEY"], "callAudioFormat":audio.suffix.lstrip(".").lower(), "recordedCall":{"callText":"", "talkGroupInfo":{"callTargets":targets,"receiver":"Trunk-Recorder " + cfg.get("SYSTEM_NAME", ""),"receiverVCO":0,"frequency":call.get("freq", ""),"sourceid":call.get("source", ""),"sourcelabel":call.get("source_description", ""),"sourcetag":"","lcn":call.get("lcn", ""),"voiceservice":call.get("voice_service", ""),"systemid":cfg.get("SYSTEM_NAME", ""),"systemlabel":"","systemtype":"","siteid":call.get("site", ""),"sitelabel":call.get("site_description", ""),"calltype":"1"},"startTime":start,"callDuration":call.get("call_length", 0),"startPositionSec":"00:00:00"}}

def attempt(item, cfg):
    audio, call = Path(item["audio"]), json.loads(Path(item["metadata"]).read_text())
    payload = json.dumps(legacy_metadata(call, cfg, audio)).encode()
    status, body = post(cfg["DESTINATION_URL"].rstrip("/")+"/api/callupload", payload, {"Content-Type":"application/json"}, int(cfg.get("TIMEOUT_SECONDS", "30")))
    response = json.loads(body or b"{}")
    if status != 200 or response.get("Status", 500) >= 400: raise RuntimeError("metadata rejected")
    token = response.get("CallAudioID")
    if response.get("Duplicate"): return
    if not token: raise RuntimeError("missing upload identifier")
    mime = "audio/mpeg" if audio.suffix.lower()==".mp3" else "audio/wav"
    status, body = post(cfg["DESTINATION_URL"].rstrip("/")+"/api/callaudioupload/"+token, audio.read_bytes(), {"Content-Type":mime}, int(cfg.get("TIMEOUT_SECONDS", "30")))
    response = json.loads(body or b"{}")
    if status != 200 or response.get("Status", 500) >= 400: raise RuntimeError("audio rejected")

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

def drain(cfg):
    root=Path(cfg["SPOOL_DIR"]); retries=int(cfg.get("RETRY_COUNT","5"))
    for item in sorted((root/"pending").glob("*.json")):
        record=json.loads(item.read_text())
        if record["next"] > time.time(): continue
        try:
            for destination in destinations(cfg): attempt(record, destination)
            item.unlink()
        except (OSError, ValueError, URLError, RuntimeError):
            record["attempts"] += 1
            if record["attempts"] > retries: shutil.move(str(item), root/"failed"/item.name)
            else: record["next"] = time.time()+min(300, 2**record["attempts"]); item.write_text(json.dumps(record))

def main():
    p=argparse.ArgumentParser(); p.add_argument("--env", required=True); p.add_argument("--audio"); p.add_argument("--metadata"); p.add_argument("--drain", action="store_true"); a=p.parse_args(); envfile(a.env); cfg=dict(os.environ)
    if a.drain: drain(cfg); return
    if not a.audio or not a.metadata: p.error("--audio and --metadata are required")
    queue(a.audio,a.metadata,cfg); drain(cfg)
if __name__ == "__main__": main()
