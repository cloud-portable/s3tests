"""The result model: Outcome, VectorResult, StepResult, CheckFailure.

Attributes are snake_case; ``to_dict``/``from_dict`` implement the shared
cross-implementation JSON contract (camelCase keys, integer-nanosecond
durations) used by the report golden fixture and by consumers exchanging
results between runners.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal, Mapping

Outcome = Literal["pass", "fail", "blocked", "skipped"]

PASS: Outcome = "pass"
FAIL: Outcome = "fail"
BLOCKED: Outcome = "blocked"
SKIPPED: Outcome = "skipped"


@dataclass
class CheckFailure:
    """One expectation mismatch, expressed as expected vs actual strings."""

    field: str  # e.g. "status", "error", "response.ETag", "headers.content-range", "body"
    expected: str
    actual: str

    def to_dict(self) -> dict[str, Any]:
        return {"field": self.field, "expected": self.expected, "actual": self.actual}

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "CheckFailure":
        return cls(str(d.get("field", "")), str(d.get("expected", "")), str(d.get("actual", "")))


@dataclass
class StepResult:
    """The observed outcome of one executed step."""

    index: int = 0  # 0-based position in the vector's steps
    kind: str = ""  # "operation" or "http"
    name: str = ""  # operation name, or "METHOD /path" for $http steps
    presigned: bool = False
    identity: str = ""  # "main", "anonymous", "invalid" or a credential handle
    status: int = 0  # raw HTTP status observed (0 if the request never completed)
    passed: bool = False
    failures: list[CheckFailure] = field(default_factory=list)  # expectation mismatches
    err: str = ""  # transport/dispatch/runner error, if any
    captured: dict[str, str] | None = None  # values captured for later steps
    duration: int = 0  # nanoseconds

    def to_dict(self) -> dict[str, Any]:
        return {
            "index": self.index,
            "kind": self.kind,
            "name": self.name,
            "presigned": self.presigned,
            "identity": self.identity,
            "status": self.status,
            "passed": self.passed,
            "failures": [f.to_dict() for f in self.failures],
            "err": self.err,
            "captured": dict(self.captured) if self.captured is not None else None,
            "duration": self.duration,
        }

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "StepResult":
        cap = d.get("captured")
        return cls(
            index=int(d.get("index", 0)),
            kind=str(d.get("kind", "")),
            name=str(d.get("name", "")),
            presigned=bool(d.get("presigned", False)),
            identity=str(d.get("identity", "")),
            status=int(d.get("status", 0)),
            passed=bool(d.get("passed", False)),
            failures=[CheckFailure.from_dict(f) for f in d.get("failures") or []],
            err=str(d.get("err", "")),
            captured={str(k): str(v) for k, v in cap.items()} if cap is not None else None,
            duration=int(d.get("duration", 0)),
        )


@dataclass
class VectorResult:
    """The outcome of one vector."""

    id: str
    group: str
    title: str = ""
    tags: list[str] = field(default_factory=list)
    # URL of the test this vector was converted from, when the corpus records
    # one — useful in reports for tracing a failure back to its origin.
    source: str = ""
    outcome: Outcome = PASS
    # Explains blocked ("prerequisite $bucket b1: ...") and skipped (the skip
    # rule's reason) outcomes.
    reason: str = ""
    # Set when a fail is a runner or vector-definition error (unsupported
    # operation, unresolvable placeholder) rather than a compatibility
    # failure of the server under test.
    runner_error: str = ""
    # Results for executed steps only (execution stops at the first failing step).
    steps: list[StepResult] = field(default_factory=list)
    # Non-fatal problems, e.g. teardown leftovers.
    warnings: list[str] = field(default_factory=list)
    # Vector wall time (including teardown) in nanoseconds.
    duration: int = 0

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "group": self.group,
            "title": self.title,
            "tags": list(self.tags),
            "source": self.source,
            "outcome": self.outcome,
            "reason": self.reason,
            "runnerError": self.runner_error,
            "steps": [s.to_dict() for s in self.steps],
            "warnings": list(self.warnings),
            "duration": self.duration,
        }

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "VectorResult":
        return cls(
            id=str(d.get("id", "")),
            group=str(d.get("group", "")),
            title=str(d.get("title", "")),
            tags=[str(t) for t in d.get("tags") or []],
            source=str(d.get("source", "")),
            outcome=d.get("outcome", PASS),
            reason=str(d.get("reason", "")),
            runner_error=str(d.get("runnerError", "")),
            steps=[StepResult.from_dict(s) for s in d.get("steps") or []],
            warnings=[str(w) for w in d.get("warnings") or []],
            duration=int(d.get("duration", 0)),
        )
