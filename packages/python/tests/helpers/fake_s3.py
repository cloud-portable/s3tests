"""Minimal in-memory S3 for exercising runner mechanics: bucket
create/delete, object put/get, empty version/upload listings, no locking.
Port of the JS runner's fake-s3 test helper."""

from __future__ import annotations

import hashlib
import re
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, unquote, urlsplit


class FakeS3:
    def __init__(self) -> None:
        self.buckets: dict[str, dict[str, bytes]] = {}  # name -> key -> bytes
        self.lock = threading.Lock()
        srv = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *args):
                pass

            def _handle(self):
                n = int(self.headers.get("content-length") or 0)
                body = self.rfile.read(n) if n else b""
                status, headers, out = srv._route(self.command, self.path, body)
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

    def _route(self, method: str, path: str, body: bytes):
        u = urlsplit(path)
        parts = u.path.lstrip("/").split("/")
        bucket = unquote(parts[0]) if parts else ""
        key = unquote("/".join(parts[1:]))
        q = parse_qs(u.query, keep_blank_values=True)
        xml = {"content-type": "application/xml"}

        def error(status, code):
            return status, xml, f'<?xml version="1.0"?><Error><Code>{code}</Code><Message>{code}</Message></Error>'.encode()

        with self.lock:
            objects = self.buckets.get(bucket)
            if key == "" and method == "PUT":  # CreateBucket
                self.buckets[bucket] = {}
                return 200, {}, b""
            if key == "" and method == "DELETE":
                self.buckets.pop(bucket, None)
                return 204, {}, b""
            if key == "" and "uploads" in q:
                return 200, xml, b'<?xml version="1.0"?><ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>'
            if key == "" and "versions" in q:
                out = '<?xml version="1.0"?><ListVersionsResult><IsTruncated>false</IsTruncated>'
                for k in (objects or {}):
                    out += f"<Version><Key>{k}</Key><VersionId>null</VersionId></Version>"
                out += "</ListVersionsResult>"
                return 200, xml, out.encode()
            if key == "" and "object-lock" in q:
                return error(404, "ObjectLockConfigurationNotFoundError")
            if key == "" and "delete" in q and method == "POST":
                for m in re.finditer(r"<Key>([^<]*)</Key>", body.decode("utf-8", "replace")):
                    if objects is not None:
                        objects.pop(m.group(1), None)
                return 200, xml, b'<?xml version="1.0"?><DeleteResult></DeleteResult>'
            if key != "" and method == "PUT":
                if objects is None:
                    return error(404, "NoSuchBucket")
                objects[key] = body
                return 200, {"ETag": f'"{hashlib.md5(body).hexdigest()}"'}, b""
            if key != "" and method in ("GET", "HEAD"):
                data = (objects or {}).get(key)
                if data is None:
                    return error(404, "NoSuchKey")
                headers = {"ETag": f'"{hashlib.md5(data).hexdigest()}"', "content-type": "application/octet-stream"}
                return 200, headers, data
            return error(400, "NotImplemented")
