"""Per-vector executor: establish prerequisites, run steps sequentially,
always tear down. Corpus vectors are shared and cached — never mutated;
interpolation rebuilds values."""

from __future__ import annotations

import re
import secrets
import time
from typing import Optional

from ._interp import Scope
from ._match import MatchError, content_value
from ._provision import BucketInfo, Target
from ._rawhttp import Cancellable
from ._result import PASS, StepResult, VectorResult
from ._run import Run, Runtime, track_key
from ._step_http import run_http_step
from ._step_operation import run_operation_step
from ._vdata import DataCache


def new_result(vector: dict, outcome: str = PASS, reason: str = "") -> VectorResult:
    """A result seeded with the vector's identifying metadata and no outcome
    detail yet — the shape shared by executed and skipped vectors."""
    return VectorResult(
        id=vector.get("id", ""),
        group=vector.get("group", ""),
        title=vector.get("title", "") or "",
        tags=list(vector.get("tags") or []),
        source=vector.get("source", "") or "",
        outcome=outcome,  # type: ignore[arg-type]
        reason=reason,
    )


class _Deadline:
    """A Cancellable that trips once its budget has elapsed (teardown's own
    timeout, independent of the run's cancellation)."""

    def __init__(self, seconds: float) -> None:
        self._until = time.monotonic() + seconds

    def is_set(self) -> bool:
        return time.monotonic() > self._until


def run_vector(rt: Runtime, vector: dict, cancel: Optional[Cancellable]) -> VectorResult:
    """Execute one vector; never raises — problems become the result's outcome."""
    started = time.perf_counter_ns()
    cache = DataCache(vector.get("data"))
    run = Run(
        rt=rt,
        vector=vector,
        cache=cache,
        scope=Scope(env={"endpoint": rt.cfg.endpoint, "region": rt.cfg.region}, data=cache.derived),
        result=new_result(vector),
        cancel=cancel,
    )
    try:
        _execute(run)
    finally:
        _teardown(run)
    run.result.duration = time.perf_counter_ns() - started
    return run.result


def _execute(run: Run) -> None:
    for prereq in run.vector.get("prerequisites") or []:
        if run.cancelled():
            return
        try:
            _establish(run, prereq)
        except Exception as err:  # noqa: BLE001 - any provisioning problem blocks
            run.result.outcome = "blocked"
            run.result.reason = str(err)
            return
    for i, step in enumerate(run.vector.get("steps") or []):
        if run.cancelled():
            return
        sr = _run_step(run, i, step)
        run.result.steps.append(sr)
        if not sr.passed:
            run.result.outcome = "fail"
            return


def _establish(run: Run, prereq: dict) -> None:
    """Provision one prerequisite and register its resource attributes."""
    rt = run.rt
    if "$bucket" in prereq:
        p = prereq["$bucket"]
        name = _bucket_name(run, p["handle"])
        try:
            info = _provisioner(rt).bucket(rt.target, p, name, run.cancel)
        except Exception as err:  # noqa: BLE001
            raise RuntimeError(f"prerequisite $bucket {p['handle']}: {err}") from err
        run.buckets.append(BucketInfo(name=info.name, known_keys=list(info.known_keys or [])))
        run.scope.res[p["handle"]] = {"name": info.name}
        return
    if "$object" in prereq:
        p = prereq["$object"]
        bucket_attrs = run.scope.res.get(p["bucket"])
        if bucket_attrs is None:
            raise RuntimeError(f"prerequisite $object {p['handle']}: unknown bucket handle {p['bucket']!r}")
        try:
            resolved = dict(p)
            resolved["key"] = run.scope.string(p["key"])
            if p.get("contentType"):
                resolved["contentType"] = run.scope.string(p["contentType"])
            if p.get("metadata"):
                resolved["metadata"] = {k: run.scope.string(v) for k, v in p["metadata"].items()}
            body = content_value(run.scope.value(p["body"]), run.cache.bytes) if "body" in p else None
        except (MatchError, ValueError) as err:
            raise RuntimeError(f"prerequisite $object {p['handle']}: {err}") from err
        try:
            info = _provisioner(rt).object(rt.target, resolved, bucket_attrs["name"], body, run.cancel)
        except Exception as err:  # noqa: BLE001
            raise RuntimeError(f"prerequisite $object {p['handle']}: {err}") from err
        run.scope.res[p["handle"]] = {"key": info.key, "etag": info.etag, "versionId": info.version_id}
        track_key(run, bucket_attrs["name"], info.key)
        return
    if "$credential" in prereq:
        handle = prereq["$credential"]["handle"]
        try:
            cred = rt.identities.provision_alt(handle)
        except Exception as err:  # noqa: BLE001
            raise RuntimeError(f"prerequisite $credential {handle}: {err}") from err
        run.scope.res[handle] = {
            "accessKeyId": cred.access_key_id or "",
            "canonicalId": cred.canonical_id or "",
            "displayName": cred.display_name or "",
        }
        return
    raise RuntimeError("prerequisite with no $bucket/$object/$credential key")


def _run_step(run: Run, index: int, step: dict) -> StepResult:
    started = time.perf_counter_ns()
    sr = StepResult(index=index)
    if "$operation" in step:
        run_operation_step(run, step["$operation"], sr)
    elif "$http" in step:
        run_http_step(run, step["$http"], sr)
    else:
        sr.err = "step with no $operation/$http key"
        run.result.runner_error = sr.err
    sr.duration = time.perf_counter_ns() - started
    sr.passed = sr.err == "" and not sr.failures
    return sr


def _provisioner(rt: Runtime):
    return rt.cfg.provisioner if rt.cfg.provisioner is not None else rt.default_provisioner


def _bucket_name(run: Run, handle: str) -> str:
    """A unique, valid bucket name: prefix + vector id + handle + random
    suffix, lowercased and trimmed to the 63-char limit."""
    name = re.sub(r"[^a-z0-9-]", "-", (run.rt.cfg.bucket_prefix + run.vector.get("id", "") + "-" + handle).lower())
    tail = "-" + secrets.token_hex(4)
    if len(name) + len(tail) > 63:
        name = name[: 63 - len(tail)]
    return name + tail


def _teardown(run: Run) -> None:
    if run.rt.cfg.keep_resources or not run.buckets:
        return
    # Cancellation must not leak buckets: teardown runs on its own deadline.
    try:
        warnings = _provisioner(run.rt).teardown(run.rt.target, run.buckets, _Deadline(120.0))
        run.result.warnings.extend(warnings)
    except Exception as err:  # noqa: BLE001
        run.result.warnings.append("teardown: " + str(err))
