"""Presigned-URL steps: mint with boto3's presigner, execute with the stdlib
HTTP client. Like the JS runner's presigner, boto3 hoists signable headers
into X-Amz-* query parameters; the corpus's presigned vectors carry no
header-bound params."""

from __future__ import annotations

import http.client
from typing import Any, Optional
from urllib.parse import urlsplit

from botocore import xform_name

from ._coerce import build_input
from ._dispatch import supported, unsupported_error
from ._match import Resolver
from ._rawhttp import RawResponse


def presign_supported(client, name: str) -> bool:
    return supported(client, name)


def presign_and_execute(
    client, name: str, params: Optional[dict[str, Any]], resolve: Optional[Resolver], expires_in: int, timeout: float
) -> RawResponse:
    """Mint a presigned request and execute it. Body bytes are held aside and
    sent by us (S3 presigned requests use UNSIGNED-PAYLOAD)."""
    if not supported(client, name):
        raise unsupported_error(name)
    model = client.meta.service_model.operation_model(name)
    kwargs, body = build_input(model, params or {}, resolve)
    method = str(model.http.get("method", "GET")).upper()
    kwargs.pop("Body", None)  # UNSIGNED-PAYLOAD: sent at execution time
    url = client.generate_presigned_url(
        ClientMethod=xform_name(name), Params=kwargs, ExpiresIn=expires_in if expires_in > 0 else 3600, HttpMethod=method
    )
    u = urlsplit(url)
    conn_cls = http.client.HTTPSConnection if u.scheme == "https" else http.client.HTTPConnection
    conn = conn_cls(u.hostname or "", u.port, timeout=timeout)
    try:
        target = u.path or "/"
        if u.query:
            target += "?" + u.query
        send_body = body if (body is not None and method not in ("GET", "HEAD")) else None
        conn.request(method, target, body=send_body)
        resp = conn.getresponse()
        data = resp.read()
        headers: dict[str, str] = {}
        for k, v in resp.getheaders():
            headers.setdefault(k.lower(), v)
        return RawResponse(resp.status, headers, data)
    finally:
        conn.close()
