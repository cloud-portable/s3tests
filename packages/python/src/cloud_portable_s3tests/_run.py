"""Per-vector run state shared by the executor and the step modules."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Optional

from ._interp import Scope
from ._provision import BucketInfo
from ._rawhttp import Cancellable
from ._result import StepResult, VectorResult
from ._vdata import DataCache


@dataclass
class Runtime:
    """What every vector run shares: config, identities, target, provisioner."""

    cfg: Any
    identities: Any
    target: Any
    default_provisioner: Any


@dataclass
class Run:
    rt: Runtime
    vector: dict
    cache: DataCache
    scope: Scope
    result: VectorResult
    cancel: Optional[Cancellable] = None
    buckets: list[BucketInfo] = field(default_factory=list)

    def cancelled(self) -> bool:
        return self.cancel is not None and self.cancel.is_set()


def runner_fail(run: Run, sr: StepResult, err: BaseException) -> None:
    """Record a runner/vector-definition error (not a compat failure)."""
    sr.err = str(err)
    run.result.runner_error = str(err)


def track_bucket(run: Run, name: str) -> None:
    """Register a bucket created by a *step* (CreateBucket, or a raw PUT on a
    bucket path) so teardown covers it like prerequisite buckets."""
    if not name or any(b.name == name for b in run.buckets):
        return
    run.buckets.append(BucketInfo(name=name))


def track_key(run: Run, bucket: str, key: str) -> None:
    """Record an object key the runner wrote, giving teardown a way to delete
    keys that server listings fail to surface."""
    if not bucket or not key:
        return
    for b in run.buckets:
        if b.name == bucket:
            if key not in b.known_keys:
                b.known_keys.append(key)
            return
