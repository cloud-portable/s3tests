"""``$http`` step execution over raw sockets."""

from __future__ import annotations

from ._config import IDENTITY_ANONYMOUS, IDENTITY_MAIN
from ._interp import InterpError
from ._jsonpath import PathError, get_string
from ._match import MatchError, content_value
from ._rawhttp import Credentials, RawHttpError, RawRequest, RawResponse, raw_request
from ._result import StepResult
from ._step_operation import evaluate_raw, raw_capture_value
from ._run import Run, runner_fail, track_bucket


def run_http_step(run: Run, src: dict, sr: StepResult) -> None:
    try:
        st = run.scope.value(src)
    except InterpError as err:
        return runner_fail(run, sr, err)
    identity = st.get("identity") or IDENTITY_MAIN
    sr.kind = "http"
    sr.name = f"{st.get('method', '')} {st.get('path', '')}"
    sr.identity = identity

    # sign defaults to true; anonymous requests are inherently unsigned.
    sign = st.get("sign", True) is not False and identity != IDENTITY_ANONYMOUS
    credentials = None
    if sign:
        try:
            cred = run.rt.identities.resolve_credentials(identity)
        except Exception as err:  # noqa: BLE001
            return runner_fail(run, sr, err)
        if cred is not None:
            credentials = Credentials(cred.access_key_id, cred.secret_access_key, cred.session_token or "")

    body = b""
    if "body" in st:
        try:
            body = content_value(st["body"], run.cache.bytes)
        except MatchError as err:
            return runner_fail(run, sr, RuntimeError(f"body: {err}"))

    try:
        res = raw_request(
            run.rt.cfg.endpoint,
            RawRequest(
                method=st["method"], path=st["path"], query=_normalize_multi(st.get("query")),
                headers=_ordered_headers(st.get("headers")), body=body, sign=sign, credentials=credentials,
                region=run.rt.cfg.region,
            ),
            run.cancel, run.rt.cfg.request_timeout_ms / 1000,
        )
    except RawHttpError as err:
        # A transport-level failure is an observation about the server, not a
        # runner bug: report it as the step's failure.
        sr.err = str(err)
        return
    sr.status = res.status
    # A successful raw PUT on a bare bucket path is a bucket creation the
    # teardown must cover.
    if st["method"] == "PUT" and 200 <= res.status < 300 and not st.get("query"):
        seg = st["path"].strip("/")
        if seg != "" and "/" not in seg:
            track_bucket(run, seg)
    evaluate_raw(run, st.get("expect"), res.status, res.headers, res.body, sr)
    if sr.failures or sr.err != "":
        return
    _capture_raw(run, st.get("capture"), res, sr)


def _capture_raw(run: Run, spec: dict | None, res: RawResponse, sr: StepResult) -> None:
    if not spec:
        return
    sr.captured = {}
    value = raw_capture_value(res.status, res.headers)
    for name, path in spec.items():
        try:
            val = get_string(value, path)
        except PathError as err:
            sr.err = f"capture {name}: {err}"
            return
        sr.captured[name] = val
        run.scope.cap[name] = val


def _normalize_multi(m: dict | None) -> dict[str, list[str]]:
    """Normalize OneOrMany (string | string[]) maps into list values."""
    if not m:
        return {}
    return {k: (list(v) if isinstance(v, list) else [v]) for k, v in m.items()}


def _ordered_headers(m: dict | None) -> list[tuple[str, str]]:
    """Flatten the step's header map into a deterministic ordered list (JSON
    objects carry no order, so keys are sorted; multi-valued headers keep
    their declared value order)."""
    if not m:
        return []
    out: list[tuple[str, str]] = []
    for name in sorted(m):
        values = m[name] if isinstance(m[name], list) else [m[name]]
        for v in values:
            out.append((name, v))
    return out
