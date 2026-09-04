"""cloud-portable-s3tests — runner for the language-independent S3 API
compatibility test vectors. Executes ``api``-kind vectors against an S3
endpoint and yields one result per vector with outcome ``pass``, ``fail``,
``blocked`` or ``skipped``.

    from cloud_portable_s3tests import Runner, Config, Credential, vectors, apply_filters, groups, tags

    runner = Runner(Config(endpoint=endpoint, credentials=Credential(access_key_id, secret_access_key)))
    selected = apply_filters(vectors(), groups("object-crud"), tags("tier-1"))
    for res in runner.run(selected):
        print(res.outcome, res.id)

``run()`` executes exactly the vectors it is given: ``apply_filters`` composes
the built-in group/tag/id filters with any custom predicate, but any
reduction of the ``vectors()`` list works. Vectors dropped this way leave no
trace in the results; to record vectors as skipped instead — keeping reports
comparable across runs — pass skip rules to ``run()``::

    runner.run(selected, skip=[skip("known bug #123", ids("multipart-0013"))])

Signing-kind corpus vectors are out of scope and never loaded.
"""

from __future__ import annotations

from cloud_portable_s3vectors import load_all

from ._filter import (
    FilterFunc, Vector, apply_filters, exclude_groups, exclude_ids, exclude_tags, groups, ids, tags,
)
from ._result import BLOCKED, FAIL, PASS, SKIPPED, CheckFailure, Outcome, StepResult, VectorResult
from ._skip import SkipFunc, skip

__all__ = [
    "Runner", "Config", "Credential", "vectors",
    "apply_filters", "groups", "tags", "ids", "exclude_groups", "exclude_tags", "exclude_ids",
    "skip", "default_provisioner", "Provisioner", "Target", "BucketInfo", "ObjectInfo",
    "Outcome", "PASS", "FAIL", "BLOCKED", "SKIPPED", "VectorResult", "StepResult", "CheckFailure",
    "FilterFunc", "SkipFunc", "Vector",
]


def vectors() -> list[Vector]:
    """Every api-kind vector in the corpus, in manifest order. The vectors are
    the corpus package's shared, cached objects — treat them as read-only."""
    return [v for f in load_all() for v in f["vectors"] if v.get("kind") == "api"]


def __getattr__(name: str):  # lazy: the runner pulls in boto3
    if name in ("Runner", "Config", "Credential", "default_provisioner", "Provisioner", "Target", "BucketInfo", "ObjectInfo"):
        from . import _config, _provision, _runner  # noqa: F401

        return {
            "Runner": _runner.Runner,
            "Config": _config.Config,
            "Credential": _config.Credential,
            "default_provisioner": _provision.default_provisioner,
            "Provisioner": _provision.Provisioner,
            "Target": _provision.Target,
            "BucketInfo": _provision.BucketInfo,
            "ObjectInfo": _provision.ObjectInfo,
        }[name]
    raise AttributeError(name)
