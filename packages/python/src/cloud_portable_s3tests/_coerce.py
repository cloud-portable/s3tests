"""Vector params (interpolated plain JSON, AWS API-model member names) →
boto3 call arguments. botocore carries the service model at runtime, so
coercion is driven by each member's declared shape: timestamps become
datetimes, the streaming ``Body`` blob becomes bytes (held aside for the
presign path), binary content descriptors for string members become base64,
numbers written for string members become strings."""

from __future__ import annotations

import base64
import json
import re
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from typing import Any, Optional

from ._match import Resolver, content_value


class CoerceError(ValueError):
    """A vector-definition problem (unparseable timestamp, bad content)."""


def build_input(operation_model, params: dict[str, Any], resolve: Optional[Resolver]) -> tuple[dict[str, Any], Optional[bytes]]:
    """Coerce interpolated vector params for ``operation_model``. Returns the
    call kwargs and the held-aside Body bytes (None when the operation sends
    no body)."""
    body: Optional[bytes] = None
    shape = operation_model.input_shape if operation_model is not None else None

    def walk(value: Any, member_shape, key: str) -> Any:
        nonlocal body
        if key == "Body" and member_shape is not None and member_shape.type_name == "blob":
            body = content_value(value, resolve)
            return body
        if key == "CopySource" and isinstance(value, dict) and isinstance(value.get("Bucket"), str) and isinstance(value.get("Key"), str):
            # boto3-style object form; composed like the Go and JS runners
            # (verbatim, no URL-escaping — the corpus pre-encodes).
            src = value["Bucket"] + "/" + value["Key"]
            if isinstance(value.get("VersionId"), str) and value["VersionId"] != "":
                src += "?versionId=" + value["VersionId"]
            return src
        t = member_shape.type_name if member_shape is not None else None
        if t == "timestamp":
            return parse_time(value) if isinstance(value, str) else value
        if t == "string":
            if isinstance(value, dict) and len(value) == 1 and ("$base64" in value or "$data" in value):
                # Binary content descriptors for string members (SSE-C keys)
                # travel base64-encoded.
                return base64.b64encode(content_value(value, resolve)).decode("ascii")
            if isinstance(value, (int, float)) and not isinstance(value, bool):
                return json.dumps(value)  # e.g. PartNumberMarker written as a bare number
            return value
        if t == "blob":
            return content_value(value, resolve) if isinstance(value, (str, dict)) else value
        if t == "structure" and isinstance(value, dict):
            return {k: walk(v, member_shape.members.get(k), k) for k, v in value.items()}
        if t == "list" and isinstance(value, list):
            return [walk(e, member_shape.member, key) for e in value]
        if t == "map" and isinstance(value, dict):
            return {k: walk(v, member_shape.value, k) for k, v in value.items()}
        return value

    members = shape.members if shape is not None else {}
    out = {k: walk(v, members.get(k), k) for k, v in params.items()}
    return out, body


_RFC3339 = re.compile(r"^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-]\d{2}:\d{2})$")
_HTTP_DATE = re.compile(r"^[A-Z][a-z]{2}, \d{2} [A-Z][a-z]{2} \d{4} \d{2}:\d{2}:\d{2} GMT$")
_BARE_DATE = re.compile(r"^(\d{4})-(\d{2})-(\d{2})$")
_COMPACT_DATE = re.compile(r"^(\d{4})(\d{2})(\d{2})$")


def parse_time(s: str) -> datetime:
    """Parse the corpus's timestamp shapes: RFC3339 (the norm), HTTP-date
    ("Expires" params) and bare dates (lifecycle rules). Raises on anything
    else (a vector-definition error → RunnerError)."""
    try:
        if _RFC3339.match(s):
            return datetime.fromisoformat(s.replace("Z", "+00:00")).astimezone(timezone.utc)
        if _HTTP_DATE.match(s):
            return parsedate_to_datetime(s).astimezone(timezone.utc)
        m = _BARE_DATE.match(s) or _COMPACT_DATE.match(s)
        if m:
            return datetime(int(m.group(1)), int(m.group(2)), int(m.group(3)), tzinfo=timezone.utc)
    except ValueError:
        pass
    raise CoerceError(f"unparseable timestamp {json.dumps(s)}")
