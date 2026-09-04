"""``$http`` steps over raw TCP/TLS sockets. The corpus's wire-level tests
send headers no HTTP client library will emit (empty authorization,
content-length "" or "-1"), so requests are serialized by hand and responses
parsed with a minimal reader. SigV4 signing is hand-rolled on the stdlib so the
runner controls exactly which headers are covered."""

from __future__ import annotations

import datetime as _dt
import hashlib
import hmac
import re
import socket
import ssl
from dataclasses import dataclass, field
from typing import Optional, Protocol
from urllib.parse import quote, urlsplit


class Cancellable(Protocol):
    def is_set(self) -> bool: ...


@dataclass
class Credentials:
    access_key_id: str
    secret_access_key: str
    session_token: str = ""


@dataclass
class RawRequest:
    method: str
    path: str
    query: dict[str, list[str]] = field(default_factory=dict)
    headers: list[tuple[str, str]] = field(default_factory=list)  # ordered step overrides
    body: bytes = b""
    sign: bool = False
    credentials: Optional[Credentials] = None
    region: str = "us-east-1"


@dataclass
class RawResponse:
    status: int
    headers: dict[str, str]  # lowercase names, first value wins
    body: bytes


class RawHttpError(Exception):
    """A transport-level problem (connect, timeout, malformed response)."""


def raw_request(endpoint: str, req: RawRequest, cancel: Cancellable | None = None, timeout: float = 60.0) -> RawResponse:
    """Assemble, optionally sign, send the request to the endpoint and read
    the response."""
    u = urlsplit(endpoint)
    host = u.netloc
    body = req.body or b""
    target = req.path
    query = encode_query(req.query)
    if query != "":
        target += "?" + query

    # Defaults, later overridden by step headers (case-insensitively; an
    # override applies even with an empty value — that is the point of the
    # wire-header tests).
    headers: list[tuple[str, str]] = [
        ("Host", host),
        ("Content-Length", str(len(body))),
        ("Connection", "close"),
    ]
    if req.sign:
        if req.credentials is None:
            raise RawHttpError("signing requested without credentials")
        headers.extend(sign_headers(host, target, req, body))
    headers = apply_overrides(headers, req.headers)

    raw = _send(u, req.method, target, headers, body, cancel, timeout)
    return parse_response(raw)


def encode_query(query: dict[str, list[str]] | None) -> str:
    if not query:
        return ""
    parts = []
    for k in sorted(query):
        for v in query[k]:
            parts.append(k + "=" + v)  # values sent as given
    return "&".join(parts)


def sign_headers(host: str, target: str, req: RawRequest, body: bytes) -> list[tuple[str, str]]:
    """SigV4-sign a shadow request carrying the step headers (so the
    signature covers them) and return the resulting auth headers.
    Content-Length is deliberately never signed: SigV4 does not require it,
    and the corpus overrides it on requests that must still authenticate."""
    raw_path, raw_query = _split_target(target)
    payload_hash = hashlib.sha256(body).hexdigest()
    shadow: dict[str, str] = {"host": host}
    for name, value in req.headers:
        lower = name.lower()
        if lower == "content-length":
            continue  # wire-only; never signed
        if lower == "x-amz-content-sha256":
            payload_hash = value
        shadow[lower] = value
    shadow["x-amz-content-sha256"] = payload_hash

    creds = req.credentials
    assert creds is not None
    now = _dt.datetime.now(_dt.timezone.utc)
    amz_date = now.strftime("%Y%m%dT%H%M%SZ")
    date_stamp = now.strftime("%Y%m%d")
    shadow["x-amz-date"] = amz_date
    if creds.session_token:
        shadow["x-amz-security-token"] = creds.session_token

    # Canonical query: keys and values URI-encoded, sorted by key then value.
    pairs: list[tuple[str, str]] = []
    if raw_query != "":
        for pair in raw_query.split("&"):
            k, _, v = pair.partition("=")
            pairs.append((_uri_encode(k), _uri_encode(v)))
    pairs.sort()
    canonical_query = "&".join(f"{k}={v}" for k, v in pairs)

    signed_names = sorted(shadow)
    canonical_headers = "".join(f"{n}:{_trim(shadow[n])}\n" for n in signed_names)
    signed_headers = ";".join(signed_names)
    # S3 canonicalizes the path as-is (no double-encoding).
    canonical_request = "\n".join(
        [req.method, raw_path, canonical_query, canonical_headers, signed_headers, payload_hash]
    )
    scope = f"{date_stamp}/{req.region}/s3/aws4_request"
    string_to_sign = "\n".join(
        ["AWS4-HMAC-SHA256", amz_date, scope, hashlib.sha256(canonical_request.encode("utf-8")).hexdigest()]
    )
    k_date = _hmac(("AWS4" + creds.secret_access_key).encode("utf-8"), date_stamp)
    k_region = _hmac(k_date, req.region)
    k_service = _hmac(k_region, "s3")
    k_signing = _hmac(k_service, "aws4_request")
    signature = hmac.new(k_signing, string_to_sign.encode("utf-8"), hashlib.sha256).hexdigest()
    authorization = (
        f"AWS4-HMAC-SHA256 Credential={creds.access_key_id}/{scope}, "
        f"SignedHeaders={signed_headers}, Signature={signature}"
    )
    out = [("X-Amz-Date", amz_date), ("X-Amz-Content-Sha256", payload_hash)]
    if creds.session_token:
        out.append(("X-Amz-Security-Token", creds.session_token))
    out.append(("Authorization", authorization))
    return out


def _hmac(key: bytes, msg: str) -> bytes:
    return hmac.new(key, msg.encode("utf-8"), hashlib.sha256).digest()


def _uri_encode(s: str) -> str:
    return quote(s, safe="-_.~")


def _trim(v: str) -> str:
    return re.sub(r"\s+", " ", v.strip())


def _split_target(target: str) -> tuple[str, str]:
    q = target.find("?")
    return (target, "") if q < 0 else (target[:q], target[q + 1 :])


def apply_overrides(base: list[tuple[str, str]], overrides: list[tuple[str, str]]) -> list[tuple[str, str]]:
    out = list(base)
    for name, value in overrides:
        lower = name.lower()
        for i, (n, _) in enumerate(out):
            if n.lower() == lower:
                out[i] = (name, value)
                break
        else:
            out.append((name, value))
    return out


def _send(u, method: str, target: str, headers: list[tuple[str, str]], body: bytes, cancel, timeout: float) -> bytes:
    port = u.port or (443 if u.scheme == "https" else 80)
    host = u.hostname or ""
    deadline = _dt.datetime.now().timestamp() + timeout
    try:
        sock = socket.create_connection((host, port), timeout=timeout)
    except OSError as err:
        raise RawHttpError(f"connect {host}:{port}: {err}") from err
    try:
        if u.scheme == "https":
            ctx = ssl.create_default_context()
            sock = ctx.wrap_socket(sock, server_hostname=host)
        head = f"{method} {target} HTTP/1.1\r\n"
        for name, value in headers:
            head += f"{name}: {value}\r\n"
        head += "\r\n"
        sock.sendall(head.encode("latin-1"))
        if body:
            sock.sendall(body)
        chunks: list[bytes] = []
        sock.settimeout(1.0)
        while True:
            if cancel is not None and cancel.is_set():
                raise RawHttpError("request aborted")
            if _dt.datetime.now().timestamp() > deadline:
                raise RawHttpError("socket timeout")
            try:
                c = sock.recv(65536)
            except socket.timeout:
                continue
            except OSError as err:
                if chunks:
                    break  # server closed abruptly after sending; use what we have
                raise RawHttpError(f"read: {err}") from err
            if not c:
                break
            chunks.append(c)
        return b"".join(chunks)
    finally:
        try:
            sock.close()
        except OSError:
            pass


_STATUS = re.compile(rb"^HTTP/\d\.\d (\d{3})")


def parse_response(raw: bytes) -> RawResponse:
    """A minimal HTTP/1.1 response reader: status line, headers (lowercase,
    first value wins), then a body framed by Transfer-Encoding: chunked,
    Content-Length, or read-to-close (we send Connection: close)."""
    sep = raw.find(b"\r\n\r\n")
    if sep < 0:
        raise RawHttpError("malformed HTTP response: no header terminator")
    lines = raw[:sep].split(b"\r\n")
    m = _STATUS.match(lines[0])
    if not m:
        raise RawHttpError("malformed HTTP status line: " + lines[0].decode("latin-1"))
    status = int(m.group(1))
    headers: dict[str, str] = {}
    for line in lines[1:]:
        colon = line.find(b":")
        if colon < 0:
            continue
        name = line[:colon].strip().decode("latin-1").lower()
        value = line[colon + 1 :].strip().decode("latin-1")
        if name not in headers:
            headers[name] = value
    body = raw[sep + 4 :]
    if "chunked" in headers.get("transfer-encoding", "").lower():
        body = _dechunk(body)
    elif "content-length" in headers:
        cl = headers["content-length"]
        if cl.isdigit():
            body = body[: int(cl)]
    return RawResponse(status, headers, body)


def _dechunk(buf: bytes) -> bytes:
    out = []
    i = 0
    while i < len(buf):
        line_end = buf.find(b"\r\n", i)
        if line_end < 0:
            break
        size_text = buf[i:line_end].split(b";")[0].strip()
        try:
            size = int(size_text, 16)
        except ValueError:
            break
        if size == 0:
            break
        out.append(buf[line_end + 2 : line_end + 2 + size])
        i = line_end + 2 + size + 2  # skip trailing CRLF
    return b"".join(out)


_ERROR_DOC = re.compile(r"<Error[\s>]")
_ENTITIES = {"amp": "&", "lt": "<", "gt": ">", "quot": '"', "#39": "'", "apos": "'"}


def parse_xml_error(body: bytes) -> tuple[str, str]:
    """Extract <Error><Code>/<Message> from an S3 XML error body; empty
    strings when the body is not an XML error document."""
    text = body.decode("utf-8", errors="replace")
    if not _ERROR_DOC.search(text):
        return "", ""

    def pick(tag: str) -> str:
        m = re.search(f"<{tag}>([^<]*)</{tag}>", text)
        return _decode_entities(m.group(1)) if m else ""

    return pick("Code"), pick("Message")


def _decode_entities(s: str) -> str:
    return re.sub(r"&(amp|lt|gt|quot|#39|apos);", lambda m: _ENTITIES[m.group(1)], s)
