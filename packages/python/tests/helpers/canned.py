"""Canned-response HTTP server recording every request (port of the `serve`
helper in dispatch.test.js and the 500-everything stub in cli.test.js)."""

from __future__ import annotations

import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Callable

Responder = Callable[[dict], tuple[int, dict[str, str], bytes]]

BOOM_XML = b'<?xml version="1.0"?><Error><Code>Boom</Code><Message>nope</Message></Error>'


class CannedServer:
    def __init__(self, responder: Responder) -> None:
        self.requests: list[dict] = []
        srv = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *args):  # noqa: D401 - silence
                pass

            def _handle(self):
                n = int(self.headers.get("content-length") or 0)
                body = self.rfile.read(n) if n else b""
                req = {
                    "method": self.command,
                    "url": self.path,
                    "headers": {k.lower(): v for k, v in self.headers.items()},
                    "body": body,
                }
                srv.requests.append(req)
                status, headers, out = responder(req)
                self.send_response(status)
                for k, v in headers.items():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(out)))
                self.end_headers()
                if self.command != "HEAD":
                    self.wfile.write(out)

            do_GET = do_HEAD = do_PUT = do_POST = do_DELETE = _handle

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self._server.server_address[1]}"
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()

    def close(self) -> None:
        self._server.shutdown()
        self._server.server_close()
        self._thread.join()


def fail500() -> CannedServer:
    """500-everything stub: vectors with prerequisites block, vectors without
    fail their first step."""
    return CannedServer(lambda req: (500, {"content-type": "application/xml"}, BOOM_XML))
