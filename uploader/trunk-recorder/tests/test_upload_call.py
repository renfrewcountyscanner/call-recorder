import importlib.util
import io
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from contextlib import redirect_stderr
from unittest.mock import patch

MODULE = Path(__file__).parents[1] / "upload_call.py"
spec = importlib.util.spec_from_file_location("upload_call", MODULE)
upload_call = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upload_call)

class QueueTests(unittest.TestCase):
    def test_queue_persists_manifest_without_copying_audio(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            audio = root / "call.wav"; audio.write_bytes(b"RIFFxxxxWAVE")
            metadata = root / "call.json"; metadata.write_text(json.dumps({"start_time": 1, "talkgroup": 1, "call_length": 1}))
            item = upload_call.queue(audio, metadata, {"SPOOL_DIR": str(root / "spool")})
            record = json.loads(item.read_text())
            self.assertEqual(record["audio"], str(audio.resolve()))
            self.assertTrue(audio.exists())

    def test_secondary_destination_is_optional(self):
        self.assertEqual(len(upload_call.destinations({})), 1)
        self.assertEqual(len(upload_call.destinations({"SECONDARY_DESTINATION_URL":"https://second", "SECONDARY_UPLOAD_ID":"id", "SECONDARY_UPLOAD_KEY":"key"})), 2)

    def test_drain_reports_failure_and_returns_false_without_leaking_key(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            audio = root / "call.wav"; audio.write_bytes(b"RIFFxxxxWAVE")
            metadata = root / "call.json"; metadata.write_text(json.dumps({"start_time": 1, "talkgroup": 1, "call_length": 1}))
            cfg = {"SPOOL_DIR": str(root / "spool"), "UPLOAD_ID": "fleetnet-ottawa", "UPLOAD_KEY": "private-key", "RETRY_COUNT": "1"}
            upload_call.queue(audio, metadata, cfg)
            stderr = io.StringIO()
            with patch.object(upload_call, "attempt", side_effect=RuntimeError("authentication failed for private-key")), redirect_stderr(stderr):
                self.assertFalse(upload_call.drain(cfg))
            output = stderr.getvalue()
            self.assertIn("sender=fleetnet-ottawa", output)
            self.assertIn("authentication failed", output)
            self.assertIn("pending=1 failed=0", output)
            self.assertNotIn("private-key", output)

    def test_drain_success_removes_item_and_returns_true(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            audio = root / "call.wav"; audio.write_bytes(b"RIFFxxxxWAVE")
            metadata = root / "call.json"; metadata.write_text(json.dumps({"start_time": 1, "talkgroup": 1, "call_length": 1}))
            cfg = {"SPOOL_DIR": str(root / "spool")}
            upload_call.queue(audio, metadata, cfg)
            with patch.object(upload_call, "attempt"):
                self.assertTrue(upload_call.drain(cfg))
            self.assertEqual(list((root / "spool" / "pending").glob("*.json")), [])

    def test_exhausted_retry_moves_item_to_failed_and_returns_false(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            audio = root / "call.wav"; audio.write_bytes(b"RIFFxxxxWAVE")
            metadata = root / "call.json"; metadata.write_text(json.dumps({"start_time": 1, "talkgroup": 1, "call_length": 1}))
            cfg = {"SPOOL_DIR": str(root / "spool"), "RETRY_COUNT": "0"}
            upload_call.queue(audio, metadata, cfg)
            with patch.object(upload_call, "attempt", side_effect=RuntimeError("rejected")), redirect_stderr(io.StringIO()):
                self.assertFalse(upload_call.drain(cfg))
            self.assertEqual(len(list((root / "spool" / "failed").glob("*.json"))), 1)

    def test_drain_command_returns_nonzero_when_spool_is_not_clear(self):
        with tempfile.TemporaryDirectory() as directory:
            env = Path(directory) / "uploader.env"
            env.write_text(f"SPOOL_DIR={directory}/spool\n")
            argv = ["upload_call.py", "--env", str(env), "--drain"]
            with patch.dict(os.environ, {}, clear=True), patch.object(sys, "argv", argv), patch.object(upload_call, "drain", return_value=False):
                self.assertEqual(upload_call.main(), 1)

if __name__ == "__main__": unittest.main()
