#!/usr/bin/env python3
"""Notify the VM after the exact release digest has been signed and verified."""

import hashlib
import hmac
import os
import re
import sys
import time
from urllib.error import HTTPError, URLError
from urllib.request import HTTPRedirectHandler, Request, build_opener

URL = 'https://deploy-wallet.tedlin.fyi/notify'


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None


def main():
    if len(sys.argv) != 2 or not re.fullmatch(r'[a-f0-9]{40}', sys.argv[1]):
        raise SystemExit('Expected a commit SHA')
    secret = os.environ['DEMO_NOTIFY_SECRET'].encode()
    if not re.fullmatch(rb'[a-f0-9]{64}', secret):
        raise SystemExit('Invalid notification secret')
    gate = os.environ.get('DEMO_NOTIFY_GATE', '')
    if not re.fullmatch(r'[a-f0-9]{64}', gate):
        raise SystemExit('Cloudflare WAF gate is required')
    revision = sys.argv[1].encode()
    opener = build_opener(NoRedirect)
    for attempt in range(3):
        stamp = str(int(time.time()))
        signature = hmac.new(secret, stamp.encode() + b'\n' + revision, hashlib.sha256).hexdigest()
        headers = {
            'Content-Type': 'text/plain',
            'X-Demo-Notify-Gate': gate,
            'X-Release-Time': stamp,
            'X-Release-Signature': 'sha256=' + signature,
        }
        request = Request(URL, revision, headers, method='POST')
        try:
            with opener.open(request, timeout=15) as response:
                if response.status != 202:
                    raise RuntimeError(f'Notification returned HTTP {response.status}')
            print('Release notification accepted; live job will verify deployment')
            return
        except (HTTPError, URLError, TimeoutError) as error:
            if isinstance(error, HTTPError):
                error.close()
            if attempt == 2:
                raise SystemExit(f'Release notification failed: {error}') from error
            time.sleep(2)


if __name__ == '__main__':
    main()
