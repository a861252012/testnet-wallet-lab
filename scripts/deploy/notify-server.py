#!/usr/bin/env python3
"""Accept a signed release notification and queue the fixed VM poll service."""

import hashlib
import hmac
from http.server import BaseHTTPRequestHandler, HTTPServer
import os
from pathlib import Path
import re
import time

TRIGGER = Path('/run/wallet-demo-notify/trigger')
WINDOW_SECONDS = 300


def make_handler(secret, trigger):
    seen = {}

    class Handler(BaseHTTPRequestHandler):
        def respond(self, status):
            self.send_response(status)
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Content-Length', '0')
            self.end_headers()

        def do_POST(self):
            if self.path != '/notify':
                return self.respond(404)
            if self.headers.get('Transfer-Encoding') or self.headers.get('Content-Length') != '40':
                return self.respond(400)
            revision = self.rfile.read(40)
            stamp = self.headers.get('X-Release-Time', '')
            signature = self.headers.get('X-Release-Signature', '')
            if not re.fullmatch(rb'[a-f0-9]{40}', revision):
                return self.respond(400)
            if not re.fullmatch(r'[0-9]{1,12}', stamp) or not re.fullmatch(r'sha256=[a-f0-9]{64}', signature):
                return self.respond(401)
            now = int(time.time())
            if abs(now - int(stamp)) > WINDOW_SECONDS:
                return self.respond(401)
            expected = hmac.new(secret, stamp.encode() + b'\n' + revision, hashlib.sha256).hexdigest()
            if not hmac.compare_digest(expected, signature[7:]):
                return self.respond(401)
            for key, expiry in tuple(seen.items()):
                if expiry < now:
                    del seen[key]
            if signature not in seen:
                try:
                    fd = os.open(trigger, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                except FileExistsError:
                    pass
                except OSError:
                    self.log_error('Could not queue release notification')
                    return self.respond(503)
                else:
                    os.close(fd)
                seen[signature] = now + WINDOW_SECONDS
            return self.respond(202)

        def do_GET(self):
            self.respond(405)

        def log_message(self, format, *args):
            # Request headers and credentials must never enter the journal.
            pass

    return Handler


class LocalServer(HTTPServer):
    def get_request(self):
        connection, address = super().get_request()
        connection.settimeout(3)
        return connection, address


def main():
    credential_dir = os.environ['CREDENTIALS_DIRECTORY']
    secret = (Path(credential_dir) / 'notify-token').read_bytes().strip()
    if not re.fullmatch(rb'[a-f0-9]{64}', secret):
        raise ValueError('Invalid notification credential')
    with LocalServer(('127.0.0.1', 8091), make_handler(secret, TRIGGER)) as server:
        server.serve_forever()


if __name__ == '__main__':
    main()
