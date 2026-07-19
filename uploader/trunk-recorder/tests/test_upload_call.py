import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

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

if __name__ == "__main__": unittest.main()
