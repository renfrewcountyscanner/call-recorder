import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

MODULE = Path(__file__).with_name("upload_call.py")
spec = importlib.util.spec_from_file_location("upload_call", MODULE)
upload_call = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upload_call)


class LegacyResponseTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        self.audio = root / "call.wav"
        self.audio.write_bytes(b"RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x00\x7e\x00\x00\x02\x00\x10\x00data\x00\x00\x00\x00")
        self.metadata = root / "call.json"
        self.metadata.write_text(json.dumps({"start_time": 1767495845, "call_length": 1, "talkgroup": 900, "talkgroup_description": "Synthetic"}))
        self.item = {"audio": str(self.audio), "metadata": str(self.metadata)}
        self.cfg = {"DESTINATION_URL": "https://logger-api.invalid", "UPLOAD_ID": "synthetic", "UPLOAD_KEY": "not-logged", "SYSTEM_NAME": "test", "TIMEOUT_SECONDS": "1"}

    def tearDown(self):
        self.tmp.cleanup()

    def test_application_error_is_not_reported_as_missing_token(self):
        with patch.object(upload_call, "_post", return_value=(200, "application/json", b'{"Status":403,"StatusMessage":"authentication failed"}')):
            with self.assertRaisesRegex(RuntimeError, "metadata rejected: http=200, status=403, message=authentication failed"):
                upload_call.attempt(self.item, self.cfg)

    def test_success_requires_token_then_uploads_audio(self):
        responses = [(200, "application/json", b'{"Status":201,"StatusMessage":"accepted","CallAudioID":"opaque-token"}'), (200, "application/json", b'{"Status":200,"StatusMessage":"completed"}')]
        with patch.object(upload_call, "_post", side_effect=responses) as post:
            upload_call.attempt(self.item, self.cfg)
        self.assertEqual(post.call_count, 2)
        self.assertIn("/api/callaudioupload/opaque-token", post.call_args_list[1].args[0])

    def test_duplicate_is_success_without_audio_request(self):
        with patch.object(upload_call, "_post", return_value=(200, "application/json", b'{"Status":200,"Duplicate":true}')) as post:
            upload_call.attempt(self.item, self.cfg)
        post.assert_called_once()

    def test_html_or_redirect_response_is_reported_safely(self):
        with patch.object(upload_call, "_post", return_value=(302, "text/html", b"<html>login</html>")):
            with self.assertRaisesRegex(RuntimeError, "metadata response was not JSON"):
                upload_call.attempt(self.item, self.cfg)

    def test_success_without_identifier_is_explicit(self):
        with patch.object(upload_call, "_post", return_value=(200, "application/json", b'{"Status":201}')):
            with self.assertRaisesRegex(RuntimeError, "metadata accepted without CallAudioID"):
                upload_call.attempt(self.item, self.cfg)


if __name__ == "__main__":
    unittest.main()
