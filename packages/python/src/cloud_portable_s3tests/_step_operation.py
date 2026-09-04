"""``$operation`` step execution (SDK dispatch and presigned paths) and
expectation evaluation shared with ``$http`` steps."""

from __future__ import annotations

from typing import Any

from ._config import IDENTITY_MAIN
from ._dispatch import DispatchResult, call
from ._interp import InterpError
from ._jsonpath import PathError, get_string
from ._match import MatchError, match_body, match_error, match_headers, match_value, render
from ._presign import presign_and_execute
from ._rawhttp import parse_xml_error
from ._result import CheckFailure, StepResult
from ._run import Run, runner_fail, track_bucket, track_key

# Statuses whose error code cannot appear on the wire (HEAD responses and
# 304s have no body) mapped to the codes they imply.
STATUS_IMPLIED_CODES: dict[int, list[str]] = {
    304: ["NotModified", ""],
    404: ["NotFound", "NoSuchKey", "NoSuchBucket"],
    405: ["MethodNotAllowed"],
    412: ["PreconditionFailed", ""],
}


def run_operation_step(run: Run, src: dict, sr: StepResult) -> None:
    try:
        op = run.scope.value(src)
    except InterpError as err:
        return runner_fail(run, sr, err)
    identity = op.get("identity") or IDENTITY_MAIN
    sr.kind = "operation"
    sr.name = op.get("name", "")
    sr.identity = identity

    if op.get("presign"):
        sr.presigned = True
        return _run_presigned_step(run, op, identity, sr)

    try:
        client = run.rt.identities.client(identity)
    except Exception as err:  # noqa: BLE001
        return runner_fail(run, sr, err)
    if run.cancelled():
        sr.err = "run cancelled"
        return
    try:
        res = call(client, op["name"], op.get("params"), run.cache.bytes)
    except Exception as err:  # noqa: BLE001 - unsupported op / undecodable params
        return runner_fail(run, sr, err)
    sr.status = res.status
    if res.err is None:
        params = op.get("params") or {}
        bucket = params.get("Bucket") if isinstance(params.get("Bucket"), str) else ""
        key = params.get("Key") if isinstance(params.get("Key"), str) else ""
        if op["name"] == "CreateBucket":
            track_bucket(run, bucket)
        if op["name"] in ("PutObject", "CopyObject", "CompleteMultipartUpload"):
            track_key(run, bucket, key)
    _evaluate_operation(run, op, res, sr)
    if sr.failures or sr.err != "":
        return
    capture(run, op.get("capture"), res.output, sr)


def capture(run: Run, spec: dict | None, value: Any, sr: StepResult) -> None:
    """Evaluate capture paths against a generic value and register the
    results for later steps."""
    if not spec:
        return
    sr.captured = {}
    for name, path in spec.items():
        try:
            val = get_string(value, path)
        except PathError as err:
            sr.err = f"capture {name}: {err}"
            return
        sr.captured[name] = val
        run.scope.cap[name] = val


def _evaluate_operation(run: Run, op: dict, res: DispatchResult, sr: StepResult) -> None:
    exp = op.get("expect")
    expects_error = exp is not None and "error" in exp

    if expects_error and res.err is None:
        sr.failures.append(CheckFailure("error", render(exp["error"]), f"success (status {res.status})"))
        return
    if not expects_error and res.err is not None:
        # A non-2xx expect.status without expect.error (e.g. 304 responses,
        # which surface as SDK errors) is still a pass when the status and
        # remaining assertions hold.
        status_tolerated = exp is not None and "status" in exp and exp["status"] == res.status
        if not status_tolerated:
            sr.failures.append(CheckFailure("error", "success", f"status {res.status}, error {res.code}: {res.msg}"))
            return
    if exp is None:
        return
    if "status" in exp and res.status != exp["status"]:
        sr.failures.append(CheckFailure("status", str(exp["status"]), str(res.status)))
    if expects_error:
        _eval_error(run, exp["error"], res.code, res.msg, res.status, op["name"].startswith("Head"), sr)
    _eval_headers(run, exp.get("headers"), res.headers, sr)
    if "response" in exp:
        for m in match_value("", exp["response"], res.output, res.output is not None):
            sr.failures.append(CheckFailure("response." + m.path, m.expected, m.actual))
    _eval_body(run, exp.get("body", _NO_BODY), res.body, sr)


_NO_BODY = object()


def _eval_error(run: Run, expected: Any, code: str, msg: str, status: int, bodyless: bool, sr: StepResult) -> None:
    try:
        mismatches = match_error(expected, code, msg)
    except MatchError as err:
        return runner_fail(run, sr, err)
    if mismatches and (bodyless or status == 304):
        # The wire response carried no error document; accept the expected
        # code when the status implies it.
        want = expected if isinstance(expected, str) else (expected.get("code", "") if isinstance(expected, dict) else "")
        implied = STATUS_IMPLIED_CODES.get(status)
        if want != "" and implied and want in implied and code in implied:
            return
    for m in mismatches:
        sr.failures.append(CheckFailure(m.path, m.expected, m.actual))


def _eval_headers(run: Run, expected: dict | None, headers: dict[str, str] | None, sr: StepResult) -> None:
    if not expected:
        return
    try:
        mismatches = match_headers(expected, headers or {})
    except MatchError as err:
        return runner_fail(run, sr, err)
    for m in mismatches:
        sr.failures.append(CheckFailure(m.path, m.expected, m.actual))


def _eval_body(run: Run, expected: Any, body: bytes | None, sr: StepResult) -> None:
    if expected is _NO_BODY:
        return
    try:
        mismatches = match_body(expected, body if body is not None else b"", run.cache.bytes)
    except MatchError as err:
        return runner_fail(run, sr, err)
    for m in mismatches:
        sr.failures.append(CheckFailure(m.path, m.expected, m.actual))


def _run_presigned_step(run: Run, op: dict, identity: str, sr: StepResult) -> None:
    """Mint a presigned URL for the operation and execute it. Expectations are
    evaluated against the raw response (the corpus never asserts `response`
    on presigned steps)."""
    try:
        client = run.rt.identities.client(identity)
    except Exception as err:  # noqa: BLE001
        return runner_fail(run, sr, err)
    try:
        res = presign_and_execute(
            client, op["name"], op.get("params"), run.cache.bytes,
            int((op.get("presign") or {}).get("expiresIn") or 0), run.rt.cfg.request_timeout_ms / 1000,
        )
    except Exception as err:  # noqa: BLE001
        sr.err = f"executing presigned request: {err}"
        return
    sr.status = res.status
    evaluate_raw(run, op.get("expect"), res.status, res.headers, res.body, sr)
    if sr.failures or sr.err != "":
        return
    capture(run, op.get("capture"), raw_capture_value(res.status, res.headers), sr)


def evaluate_raw(run: Run, exp: dict | None, status: int, headers: dict[str, str], body: bytes, sr: StepResult) -> None:
    """Check expectations for steps executed outside the SDK ($http and
    presigned): status, headers, XML error body, body bytes."""
    expects_error = exp is not None and "error" in exp
    code, msg = parse_xml_error(body)

    if expects_error and status < 400 and code == "":
        sr.failures.append(CheckFailure("error", render(exp["error"]), f"success (status {status})"))
        return
    if not expects_error and (exp is None or "status" not in exp) and (status < 200 or status > 299):
        sr.failures.append(CheckFailure("status", "2xx", f"{status} {code} {msg}"))
        return
    if exp is None:
        return
    if "status" in exp and status != exp["status"]:
        sr.failures.append(CheckFailure("status", str(exp["status"]), str(status)))
    if expects_error:
        _eval_error(run, exp["error"], code, msg, status, False, sr)
    _eval_headers(run, exp.get("headers"), headers, sr)
    if "response" in exp:
        return runner_fail(run, sr, RuntimeError("expect.response is not supported on raw HTTP/presigned steps"))
    _eval_body(run, exp.get("body", _NO_BODY), body, sr)


def raw_capture_value(status: int, headers: dict[str, str]) -> dict[str, Any]:
    """The capture-path root for $http/presigned steps: {status, headers}
    with lowercased header names (first value)."""
    return {"status": status, "headers": dict(headers)}
