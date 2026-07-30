#!/usr/bin/env python3
"""Minimal OpenAI-compatible transcription provider for integration tests."""
import http.server
import json
import sys

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        print(fmt % args, file=sys.stderr)

    def do_POST(self):
        content_type = self.headers.get('Content-Type', '')
        length = int(self.headers.get('Content-Length', 0))
        if length > 0:
            self.rfile.read(length)
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps({"text": "synthetic transcript"}).encode())

if __name__ == '__main__':
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8080
    server = http.server.HTTPServer(('0.0.0.0', port), Handler)
    print(f"fake transcriber listening on {port}", file=sys.stderr)
    server.serve_forever()
