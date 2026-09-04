"""Matcher engine implementing the vector matcher semantics: scalar equality,
recursive subset objects, exact-length ordered arrays, assertion objects
(``$exists``/``$absent``/``$eq``/``$ne``/``$matches``/``$length``/``$contains``),
plus the header, error and body (content-descriptor / digest) expectation
forms."""

from __future__ import annotations

import base64
import binascii
import hashlib
import json
import re
from dataclasses import dataclass
from typing import Any, Callable

Resolver = Callable[[str], bytes]

_ABSENT = object()


class MatchError(ValueError):
    """A malformed expectation (a vector-definition error)."""


@dataclass
class Mismatch:
    path: str
    expected: str
    actual: str


def match_value(path: str, expected: Any, actual: Any, present: bool) -> list[Mismatch]:
    """Match a decoded expected matcher against an actual value.

    ``present=False`` means the addressed value does not exist in the response.
    """
    if _is_object(expected):
        if _is_assertion(expected):
            return _assertion(path, expected, actual, present)
        if not present:
            return _miss(path, expected, _ABSENT)
        if not _is_object(actual):
            return _miss(path, expected, actual)
        out: list[Mismatch] = []
        for k, ev in expected.items():
            has = k in actual and actual[k] is not None
            out.extend(match_value(_join(path, k), ev, actual[k] if has else None, has))
        return out
    if isinstance(expected, list):
        if not present:
            return _miss(path, expected, _ABSENT)
        if not isinstance(actual, list):
            return _miss(path, expected, actual)
        if len(expected) != len(actual):
            return [Mismatch(path, f"array of length {len(expected)}", f"array of length {len(actual)}")]
        out = []
        for i, e in enumerate(expected):
            out.extend(match_value(f"{path}[{i}]", e, actual[i], True))
        return out
    # Scalar literal: exact equality.
    if not present:
        return _miss(path, expected, _ABSENT)
    if not _scalar_equal(expected, actual):
        return _miss(path, expected, actual)
    return []


def _is_object(v: Any) -> bool:
    return isinstance(v, dict)


def _join(path: str, key: str) -> str:
    return key if path == "" else path + "." + key


def _is_assertion(m: dict) -> bool:
    return len(m) > 0 and all(isinstance(k, str) and k.startswith("$") for k in m)


def _assertion(path: str, m: dict, actual: Any, present: bool) -> list[Mismatch]:
    out: list[Mismatch] = []
    for op, arg in m.items():
        if op == "$exists":
            if present != bool(arg):
                out.append(Mismatch(path, f"exists: {_js_bool(bool(arg))}", _presence(actual, present)))
        elif op == "$absent":
            if present == bool(arg):
                out.append(Mismatch(path, f"absent: {_js_bool(bool(arg))}", _presence(actual, present)))
        elif op == "$eq":
            if not present or not _literal_equal(arg, actual):
                out.extend(_miss(path, arg, actual if present else _ABSENT))
        elif op == "$ne":
            # Scalar inequality; an absent value fails (the assertion demands a
            # differing value).
            if not present or _scalar_equal(arg, actual):
                out.append(Mismatch(path, "not equal to " + render(arg), _presence(actual, present)))
        elif op == "$matches":
            pat = arg if isinstance(arg, str) else ""
            s = _scalar_string(actual)
            if not present or s is None:
                out.append(Mismatch(path, "matches " + json.dumps(pat), _presence(actual, present)))
                continue
            try:
                matched = _regex(pat).search(s) is not None
            except re.error as err:
                out.append(Mismatch(path, "matches " + json.dumps(pat), "invalid regex: " + str(err)))
                continue
            if not matched:
                out.append(Mismatch(path, "matches " + json.dumps(pat), json.dumps(s, ensure_ascii=False)))
        elif op == "$length":
            n = _length_of(actual)
            if not present or not _is_number(arg) or n is None or n != arg:
                out.append(Mismatch(path, f"length {render(arg)}", _length_actual(actual, present)))
        elif op == "$contains":
            if not present or not isinstance(actual, list):
                out.append(Mismatch(path, "array containing " + render(arg), _presence(actual, present)))
                continue
            found = any(len(match_value(path, arg, el, True)) == 0 for el in actual)
            if not found:
                out.append(
                    Mismatch(path, "some element matching " + render(arg), f"no match among {len(actual)} element(s)")
                )
        else:
            out.append(Mismatch(path, "known assertion", "unknown assertion operator " + op))
    return out


def _literal_equal(expected: Any, actual: Any) -> bool:
    """Deep equality with numeric normalization ($eq semantics)."""
    if _is_object(expected):
        if not _is_object(actual) or len(actual) != len(expected):
            return False
        return all(k in actual and _literal_equal(v, actual[k]) for k, v in expected.items())
    if isinstance(expected, list):
        return (
            isinstance(actual, list)
            and len(actual) == len(expected)
            and all(_literal_equal(e, actual[i]) for i, e in enumerate(expected))
        )
    return _scalar_equal(expected, actual)


def _is_number(v: Any) -> bool:
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def _scalar_equal(expected: Any, actual: Any) -> bool:
    if _is_number(expected):
        return _is_number(actual) and expected == actual
    if isinstance(expected, str):
        return isinstance(actual, str) and actual == expected
    if isinstance(expected, bool):
        return isinstance(actual, bool) and actual == expected
    if expected is None:
        return actual is None
    return False


def _scalar_string(v: Any) -> str | None:
    if isinstance(v, str):
        return v
    if isinstance(v, bool):
        return _js_bool(v)
    if _is_number(v):
        return render(v)
    return None


def _length_of(v: Any) -> int | None:
    if isinstance(v, (list, str)):
        return len(v)
    return None


def _length_actual(v: Any, present: bool) -> str:
    if not present:
        return "(absent)"
    n = _length_of(v)
    return f"{_js_typeof(v)} (no length)" if n is None else f"length {n}"


def _js_typeof(v: Any) -> str:
    if v is None:
        return "object"
    if isinstance(v, bool):
        return "boolean"
    if _is_number(v):
        return "number"
    if isinstance(v, str):
        return "string"
    return "object"


def _js_bool(b: bool) -> str:
    return "true" if b else "false"


def _presence(v: Any, present: bool) -> str:
    return render(v) if present else "(absent)"


def _miss(path: str, expected: Any, actual: Any) -> list[Mismatch]:
    return [Mismatch(path, render(expected), "(absent)" if actual is _ABSENT else render(actual))]


def render(v: Any) -> str:
    """Render a value compactly for mismatch output (mirrors the Go and JS
    renderers: JSON without whitespace, truncated at 256 characters)."""
    try:
        s = json.dumps(v, separators=(",", ":"), ensure_ascii=False)
    except (TypeError, ValueError):
        s = str(v)
    if len(s) > 256:
        s = s[:256] + "…"
    return s


# $matches patterns are the portable ECMA-262 ∩ RE2 subset per the spec, so
# the stdlib re module compiles them; matching is unanchored.
_regex_cache: dict[str, "re.Pattern[str]"] = {}


def _regex(pattern: str) -> "re.Pattern[str]":
    r = _regex_cache.get(pattern)
    if r is None:
        r = re.compile(pattern)
        _regex_cache[pattern] = r
    return r


def compile_regex(pattern: str) -> None:
    """Report template validity for the offline corpus smoke test."""
    re.compile(pattern)


def match_headers(expected: dict[str, Any], headers: dict[str, str]) -> list[Mismatch]:
    """Match ``expect.headers`` (lowercase name -> matcher) against actual
    response headers (a lowercase-keyed dict of first values)."""
    out: list[Mismatch] = []
    for name, matcher in expected.items():
        present = name in headers
        out.extend(match_value("headers." + name, matcher, headers.get(name) if present else None, present))
    return out


def match_error(expected: Any, code: str, message: str) -> list[Mismatch]:
    """Match ``expect.error`` (a code string, or {code, message}) against the
    observed error code/message."""
    if isinstance(expected, str):
        if code != expected:
            return [Mismatch("error", expected, _or_empty(code))]
        return []
    if _is_object(expected):
        out: list[Mismatch] = []
        if isinstance(expected.get("code"), str) and code != expected["code"]:
            out.append(Mismatch("error.code", expected["code"], _or_empty(code)))
        if "message" in expected:
            out.extend(match_value("error.message", expected["message"], message, message != ""))
        return out
    raise MatchError("expect.error: unsupported form " + render(expected))


def match_body(expected: Any, body: bytes, resolve: Resolver | None) -> list[Mismatch]:
    """Match ``expect.body`` — either a content descriptor (exact bytes) or a
    digest assertion {$size,$md5,$sha256} — against the actual body bytes."""
    if _is_object(expected) and _is_assertion(expected) and "$data" not in expected and "$base64" not in expected:
        return _digest_body(expected, body)
    want = content_value(expected, resolve)
    if want != body:
        return [Mismatch("body", _summarize(want), _summarize(body))]
    return []


def _digest_body(m: dict, body: bytes) -> list[Mismatch]:
    out: list[Mismatch] = []
    for op, arg in m.items():
        if op == "$size":
            if arg != len(body):
                out.append(Mismatch("body.$size", render(arg), str(len(body))))
        elif op == "$md5":
            got = hashlib.md5(body).hexdigest()
            if got != arg:
                out.append(Mismatch("body.$md5", render(arg), got))
        elif op == "$sha256":
            got = hashlib.sha256(body).hexdigest()
            if got != arg:
                out.append(Mismatch("body.$sha256", render(arg), got))
        else:
            raise MatchError("expect.body: unknown digest assertion " + op)
    return out


def content_value(v: Any, resolve: Resolver | None) -> bytes:
    """Decode a content descriptor — plain string (UTF-8), {"$data": name} or
    {"$base64": "..."} — into bytes."""
    if isinstance(v, str):
        return v.encode("utf-8")
    if _is_object(v) and len(v) == 1:
        if isinstance(v.get("$data"), str):
            if resolve is None:
                raise MatchError(
                    f"content descriptor references dataset {json.dumps(v['$data'])} but the vector declares no data"
                )
            return resolve(v["$data"])
        if isinstance(v.get("$base64"), str):
            try:
                return base64.b64decode(v["$base64"], validate=True)
            except (binascii.Error, ValueError) as err:
                raise MatchError("bad $base64 content: " + str(err)) from err
    raise MatchError("invalid content descriptor: " + render(v))


def _summarize(b: bytes) -> str:
    s = f"{len(b)} bytes, md5 {hashlib.md5(b).hexdigest()}"
    if len(b) <= 64 and all(0x20 <= c <= 0x7E for c in b):
        s += f" ({json.dumps(b.decode('ascii'))})"
    return s


def _or_empty(s: str) -> str:
    return "(no error)" if s == "" else s
