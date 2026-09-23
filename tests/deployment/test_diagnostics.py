import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('diagnostics', Path(__file__).resolve().parents[2] / 'scripts/deploy/record-health.py')
diag = importlib.util.module_from_spec(spec)
spec.loader.exec_module(diag)


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(502 if self.path == '/bad' else 200)
        self.send_header('X-App-Version', 'a' * 40)
        self.send_header('CF-Ray', 'abc-TPE')
        self.end_headers()
        self.wfile.write(b'password=DO_NOT_LOG private_key=DO_NOT_LOG')

    def log_message(self, *args):
        pass


class DiagnosticsTest(unittest.TestCase):
    def test_real_http_success_and_gateway_failure_never_record_body(self):
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        try:
            for path, status in [('/', 200), ('/bad', 502)]:
                result = diag.health(f'http://127.0.0.1:{server.server_port}{path}')
                self.assertEqual(result['status'], status)
                self.assertEqual(result['revision'], 'a' * 40)
                self.assertNotIn('DO_NOT_LOG', json.dumps(result))
        finally:
            server.shutdown()
            thread.join()
            server.server_close()

    def test_timeout_and_command_failure_hide_sensitive_messages(self):
        with patch.object(diag.subprocess, 'run', side_effect=subprocess.TimeoutExpired('secret', 5)):
            self.assertEqual(diag.command(['docker']), {'error': 'TimeoutExpired'})
        with patch.object(diag.subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, 'secret', 'password')):
            self.assertEqual(diag.command(['docker']), {'error': 'exit', 'code': 1})
        with patch.object(diag.urllib.request.OpenerDirector, 'open', side_effect=TimeoutError('secret')):
            result = diag.health('http://127.0.0.1')
            self.assertEqual(result['error'], 'TimeoutError')
            self.assertNotIn('secret', json.dumps(result))

    def test_rotation_is_bounded_and_latest_failure_retained(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'health.jsonl'
            for i in range(30):
                diag.record({'sequence': i, 'local': 200, 'public': 502}, path, max_bytes=200)
            self.assertLessEqual(len(list(Path(directory).iterdir())), 4)
            self.assertEqual(json.loads(path.read_text().splitlines()[-1])['sequence'], 29)

    def test_snapshot_records_oom_restart_and_tunnel_failure_without_inspecting_env(self):
        reads = {'/proc/meminfo': 'MemTotal: 1024 kB\nMemAvailable: 10 kB\nSecret: 123 kB\n',
                 '/proc/sys/kernel/random/boot_id': 'test-boot', '/proc/uptime': '100 10', '/proc/loadavg': '1 2 3 1/10 9'}
        commands = []
        def command(args):
            commands.append(args)
            if args[:3] == ['docker', 'ps', '-aq']:
                return {'output': 'abcd1234\n'}
            if args[:2] == ['docker', 'inspect']:
                return {'output': '{"oom":true,"restarts":2}'}
            if args[:2] == ['docker', 'events']:
                return {'output': '100 oom abcd1234\n101 die abcd1234'}
            return {'output': 'failed'}
        with patch.object(Path, 'read_text', lambda p: reads[str(p)]), patch.object(diag, 'command', side_effect=command), patch.object(diag, 'health', side_effect=[{'status': 200}, {'status': 502}]):
            result = diag.snapshot()
        self.assertEqual(result['local']['status'], 200)
        self.assertEqual(result['public']['status'], 502)
        self.assertIn('oom', result['events']['output'])
        self.assertNotIn('Secret', result['memory'])
        inspect = next(c for c in commands if c[:2] == ['docker', 'inspect'])
        self.assertIn('--format', inspect)
        self.assertNotIn('.Config', ' '.join(inspect))

    def test_redirect_is_not_followed(self):
        self.assertIsNone(diag.NoRedirect().redirect_request(None, None, 302, '', {}, 'http://other/secret'))


if __name__ == '__main__':
    unittest.main()
