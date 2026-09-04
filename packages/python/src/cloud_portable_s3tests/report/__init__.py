"""Reporters over a run's results: ``junit`` and ``html`` write file reports
(``write(w, results, meta)``), ``unittest`` maps results onto subtests. All
accept any iterable of VectorResult — a list, or the live ``Runner.run()``
generator to report while running."""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Mapping

__all__ = ["Meta"]


@dataclass
class Meta:
    """Report provenance stamped into file reports."""

    corpus_version: str = ""
    target: str = ""
    properties: dict[str, str] = field(default_factory=dict)
    generated_at: datetime | None = None
    # Leave skipped vectors out of the report entirely.
    omit_skipped: bool = False

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "Meta":
        ga = d.get("generatedAt")
        return cls(
            corpus_version=str(d.get("corpusVersion", "") or ""),
            target=str(d.get("target", "") or ""),
            properties={str(k): str(v) for k, v in (d.get("properties") or {}).items()},
            generated_at=datetime.fromisoformat(ga) if isinstance(ga, str) and ga else None,
            omit_skipped=bool(d.get("omitSkipped", False)),
        )
