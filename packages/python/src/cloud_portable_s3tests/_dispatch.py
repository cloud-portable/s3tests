"""``$operation`` dispatch onto boto3: dynamic method lookup, raw
status/header capture on success AND error paths, generic response walking
for the matcher engine, and error mapping to {status, code, msg}."""

from __future__ import annotations

import base64
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Optional

from botocore import xform_name
from botocore.exceptions import ClientError
from botocore.response import StreamingBody

from ._coerce import build_input
from ._match import Resolver
from ._timefmt import utc


class UnsupportedOperation(RuntimeError):
    pass


def supported(client, name: str) -> bool:
    """Whether the operation exists in botocore's S3 model."""
    return name in client.meta.service_model.operation_names


def unsupported_error(name: str) -> UnsupportedOperation:
    return UnsupportedOperation(f"operation {name} is not supported by boto3")


@dataclass
class DispatchResult:
    status: int = 0
    headers: dict[str, str] = field(default_factory=dict)
    output: Any = None
    body: Optional[bytes] = None
    err: Optional[BaseException] = None
    code: str = ""
    msg: str = ""


def call(client, name: str, params: Optional[dict[str, Any]], resolve: Optional[Resolver]) -> DispatchResult:
    """Execute one operation. Raises only for *runner* problems (unsupported
    operation, undecodable params); server-side failures are reported inside
    the result."""
    if not supported(client, name):
        raise unsupported_error(name)
    model = client.meta.service_model.operation_model(name)
    kwargs, _ = build_input(model, params or {}, resolve)
    method = getattr(client, xform_name(name))
    res = DispatchResult()
    try:
        out = method(**kwargs)
    except ClientError as err:
        meta = err.response.get("ResponseMetadata", {}) or {}
        res.status = int(meta.get("HTTPStatusCode", 0) or 0)
        res.headers = _lower(meta.get("HTTPHeaders", {}))
        res.err = err
        res.code, res.msg = map_error(err)
        return res
    except Exception as err:  # noqa: BLE001 - transport/serialization problems are observations
        res.err = err
        res.msg = str(err)
        return res
    meta = out.get("ResponseMetadata", {}) if isinstance(out, dict) else {}
    res.status = int(meta.get("HTTPStatusCode", 0) or 0)
    res.headers = _lower(meta.get("HTTPHeaders", {}))
    res.output = walk_output(out, res)
    return res


def _lower(headers: Any) -> dict[str, str]:
    return {str(k).lower(): str(v) for k, v in (headers or {}).items()}


# Codes botocore synthesizes for bodyless error responses, mapped to the
# names the Go and JS SDKs surface so the shared status→code fallback applies.
_STATUS_CODES = {"304": "NotModified", "404": "NotFound", "405": "MethodNotAllowed", "412": "PreconditionFailed"}


def map_error(err: ClientError) -> tuple[str, str]:
    error = err.response.get("Error", {}) or {}
    code = str(error.get("Code", "") or "")
    if code.isdigit():
        code = _STATUS_CODES.get(code, "")
    if code == "Error":
        code = ""
    msg = str(error.get("Message", "") or "")
    return code, msg


def walk_output(v: Any, res: DispatchResult) -> Any:
    """Convert a boto3 response into a generic JSON-like value for the matcher
    engine: ResponseMetadata dropped, streaming bodies drained into res.body
    and excluded, datetimes rendered like Go's RFC3339Nano, binary as base64,
    None members skipped (so {"$absent": true} works)."""
    if v is None:
        return None
    if isinstance(v, datetime):
        return go_rfc3339_nano(v)
    if isinstance(v, (bytes, bytearray)):
        return base64.b64encode(bytes(v)).decode("ascii")
    if isinstance(v, StreamingBody):
        res.body = v.read()
        return None
    if isinstance(v, list):
        return [walk_output(e, res) for e in v]
    if isinstance(v, dict):
        out = {}
        for k, e in v.items():
            if k == "ResponseMetadata":
                continue
            w = walk_output(e, res)
            if w is not None:
                out[k] = w
        return out
    return v


def go_rfc3339_nano(d: datetime) -> str:
    """Render a datetime exactly like Go's time.RFC3339Nano formatting of a
    UTC time at millisecond precision (as the JS runner's Date allows):
    fractional seconds trimmed of trailing zeros and omitted when zero."""
    d = utc(d)
    s = f"{d.year:04d}-{d.month:02d}-{d.day:02d}T{d.hour:02d}:{d.minute:02d}:{d.second:02d}"
    ms = d.microsecond // 1000
    if ms != 0:
        s += "." + f"{ms:03d}".rstrip("0")
    return s + "Z"
