"""Capture-path grammar: ``path = ident ("." ident | "[" digits "]")*``,
evaluated against a generic JSON-like value."""

from __future__ import annotations

import json
from typing import Any, Union


class PathError(ValueError):
    """A malformed capture path or one that does not resolve."""


Segment = Union[str, int]  # field name or array index


def parse(path: str) -> list[Segment]:
    """Validate a capture path and return its segments."""
    if path == "":
        raise PathError("empty capture path")
    segs: list[Segment] = []
    i = 0
    n = len(path)

    def ident() -> str:
        nonlocal i
        start = i
        while i < n and path[i] != "." and path[i] != "[":
            if path[i] == "]":
                raise PathError(f'capture path {json.dumps(path)}: unexpected "]" at {i}')
            i += 1
        if i == start:
            raise PathError(f"capture path {json.dumps(path)}: empty identifier at {start}")
        return path[start:i]

    segs.append(ident())
    while i < n:
        c = path[i]
        if c == ".":
            i += 1
            segs.append(ident())
        elif c == "[":
            i += 1
            close = path.find("]", i)
            if close < 0:
                raise PathError(f"capture path {json.dumps(path)}: unterminated index")
            digits = path[i:close]
            if not digits.isdigit() or not digits.isascii():
                raise PathError(f"capture path {json.dumps(path)}: bad index {json.dumps(digits)}")
            segs.append(int(digits))
            i = close + 1
        else:
            raise PathError(f"capture path {json.dumps(path)}: unexpected {json.dumps(c)} at {i}")
    return segs


def get(v: Any, path: str) -> Any:
    """Evaluate ``path`` against ``v`` and return the addressed value."""
    cur = v
    for seg in parse(path):
        if isinstance(seg, int):
            if not isinstance(cur, list):
                raise PathError(f"capture path {json.dumps(path)}: [{seg}] applied to non-array")
            if seg >= len(cur):
                raise PathError(f"capture path {json.dumps(path)}: index {seg} out of range (len {len(cur)})")
            cur = cur[seg]
            continue
        if not isinstance(cur, dict):
            raise PathError(f"capture path {json.dumps(path)}: field {json.dumps(seg)} applied to non-object")
        if seg not in cur:
            raise PathError(f"capture path {json.dumps(path)}: no field {json.dumps(seg)} in response")
        cur = cur[seg]
    return cur


def get_string(v: Any, path: str) -> str:
    """Evaluate ``path`` and render the result as the string form used for
    ``${cap.<name>}`` substitution."""
    got = get(v, path)
    if isinstance(got, str):
        return got
    if isinstance(got, bool):
        return "true" if got else "false"
    if isinstance(got, (int, float)):
        return render_number(got)
    if got is None:
        raise PathError(f"capture path {json.dumps(path)}: value is null")
    kind = "object" if isinstance(got, dict) else "array" if isinstance(got, list) else type(got).__name__
    raise PathError(f"capture path {json.dumps(path)}: cannot capture {kind} as a string")


def render_number(n: Union[int, float]) -> str:
    """Render a number the way JS ``String(n)`` does for the values S3
    responses carry (integers without a fractional part)."""
    if isinstance(n, float) and n.is_integer():
        return str(int(n))
    return str(n)
