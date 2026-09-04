"""Raw TCP server for the $http transport tests: captures the request head
verbatim and replies with a canned response (port of rawhttp.test.js)."""

from __future__ import annotations

import socket
import threading

OK_RESPONSE = b"HTTP/1.1 200 OK\r\nx-amz-request-id: R1\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"


class RawServer:
    def __init__(self, response: bytes = OK_RESPONSE) -> None:
        self._response = response
        self._captured = bytearray()
        self._sock = socket.socket()
        self._sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self._sock.bind(("127.0.0.1", 0))
        self._sock.listen(8)
        self._sock.settimeout(0.2)
        self._stop = threading.Event()
        self.url = f"http://127.0.0.1:{self._sock.getsockname()[1]}"
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()

    def _serve(self) -> None:
        while not self._stop.is_set():
            try:
                conn, _ = self._sock.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            with conn:
                conn.settimeout(2.0)
                buf = bytearray()
                while b"\r\n\r\n" not in buf:
                    try:
                        c = conn.recv(65536)
                    except socket.timeout:
                        break
                    if not c:
                        break
                    buf += c
                self._captured += buf
                try:
                    conn.sendall(self._response)
                except OSError:
                    pass

    def head(self) -> str:
        return bytes(self._captured).decode("latin-1")

    def close(self) -> None:
        self._stop.set()
        self._thread.join()
        self._sock.close()
