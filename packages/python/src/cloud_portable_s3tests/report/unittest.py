"""unittest adapter: report each vector result as a subtest, so the stdlib
test runner itself renders the outcome (parity with the Go runner's
report/gotest and the JS runner's report/nodetest)::

    import unittest
    from cloud_portable_s3tests import Runner, vectors, apply_filters, groups
    from cloud_portable_s3tests.report import unittest as s3unittest

    class TestS3Compat(unittest.TestCase):
        def test_object_crud(self):
            runner = Runner(config)
            s3unittest.run(self, runner.run(apply_filters(vectors(), groups("object-crud"))))

Note that unittest name filters select which subtests are *reported*, not
which vectors *execute* — the runner has already produced every result by the
time its subtest runs. Select vectors before the run (apply_filters)."""

from __future__ import annotations

import unittest as _unittest
from typing import Callable, Iterable, Optional

from .._result import VectorResult
from .._timefmt import secs3

Log = Optional[Callable[[str], None]]


def run(tc: _unittest.TestCase, results: Iterable[VectorResult], *, log: Log = None) -> None:
    """Report each vector result as a subtest of ``tc``, named by the vector
    id. A pass returns (logging title and the vector's real duration through
    ``log`` when given), a fail calls ``tc.fail`` with the failing step's
    expected/actual detail, a runner error raises (a unittest *error*), and
    blocked or skipped vectors skip the subtest with a prefixed reason."""
    for res in results:
        with tc.subTest(res.id):
            report(tc, res, log=log)


def report(tc: _unittest.TestCase, res: VectorResult, *, log: Log = None) -> None:
    """Map one result onto the running test case."""
    emit = log or (lambda _msg: None)
    for w in res.warnings:
        emit(f"warning: {w}")
    if res.outcome == "pass":
        # The subtest's own wall time is ~0 (execution already happened in
        # the runner), so log the vector's real duration.
        emit(f"{res.title} ({secs3(res.duration)}s)")
        return
    if res.outcome == "fail":
        if res.runner_error:
            raise RuntimeError(f"runner error: {res.runner_error}")
        tc.fail(failure_detail(res))
    if res.outcome == "blocked":
        tc.skipTest(f"blocked: {res.reason}")
    if res.outcome == "skipped":
        tc.skipTest(f"skipped: {res.reason}")
    raise RuntimeError(f"unknown outcome {res.outcome!r}")


def failure_detail(res: VectorResult) -> str:
    """The failing (last executed) step: a "step N (name):" header followed by
    one line per expectation mismatch (identical text to the Go and JS
    adapters)."""
    if not res.steps:
        return res.reason or "failed"
    step = res.steps[-1]
    out = f"step {step.index + 1} ({step.name}):"
    if step.err:
        out += f"\n  {step.err}"
    for f in step.failures:
        out += f"\n  {f.field}: expected {f.expected}, got {f.actual}"
    return out
