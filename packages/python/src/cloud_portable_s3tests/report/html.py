"""HTML reporter rendering the shared cross-implementation mustache template
(``page.mustache`` — a synced copy of ``shared/report/page.mustache``). The
view model and all number formatting follow the shared contract so identical
results render byte-identical pages in every language, enforced by the shared
golden test (``shared/report/fixture.json`` + ``golden.html``; the Go
implementation is canonical)."""

from __future__ import annotations

from dataclasses import dataclass
from importlib import resources
from typing import Any, BinaryIO, Iterable

from .._result import VectorResult
from .._timefmt import secs1, secs3, utc
from . import Meta
from ._mustache import Template

_template = Template(resources.files(__package__).joinpath("page.mustache").read_text("utf-8"))


def write(w: BinaryIO, results: Iterable[VectorResult], meta: Meta | None = None) -> None:
    """Consume the results and write the report page (UTF-8) to ``w``."""
    w.write(render(results, meta).encode("utf-8"))


def render(results: Iterable[VectorResult], meta: Meta | None = None) -> str:
    return _template.render(view_model(results, meta or Meta()))


@dataclass
class _Counts:
    pass_: int = 0
    fail: int = 0
    blocked: int = 0
    errors: int = 0
    skipped: int = 0
    total: int = 0

    def add(self, res: VectorResult) -> None:
        self.total += 1
        if res.outcome == "pass":
            self.pass_ += 1
        elif res.outcome == "fail":
            if res.runner_error:
                self.errors += 1
            else:
                self.fail += 1
        elif res.outcome == "blocked":
            self.blocked += 1
        elif res.outcome == "skipped":
            self.skipped += 1

    @property
    def attempted(self) -> int:
        return self.total - self.skipped

    def view(self) -> dict[str, Any]:
        return {
            "pass": self.pass_,
            "fail": self.fail,
            "blocked": self.blocked,
            "errors": self.errors,
            "skipped": self.skipped,
            "total": self.total,
            "attempted": self.attempted,
            "passPct": self.pass_pct(),
            "hasFail": self.fail > 0,
            "hasBlocked": self.blocked > 0,
            "hasErrors": self.errors > 0,
            "hasSkipped": self.skipped > 0,
            "failZero": self.fail == 0,
            "blockedZero": self.blocked == 0,
            "errorsZero": self.errors == 0,
            "skippedZero": self.skipped == 0,
        }

    # Shared integer-arithmetic formatting rules (see the Go implementation).
    def pass_pct(self) -> str:
        a = self.attempted
        if a == 0:
            return "—"
        p10 = (1000 * self.pass_ + a // 2) // a
        return f"{p10 // 10}.{p10 % 10}%"

    def pct_class(self) -> str:
        a = self.attempted
        if a == 0:
            return ""
        if 100 * self.pass_ >= 80 * a:
            return "high"
        if 100 * self.pass_ >= 50 * a:
            return "medium"
        return "low"

    def bar_width(self) -> str:
        a = self.attempted
        if a == 0:
            return "0"
        return str((100 * self.pass_ + a // 2) // a)


def view_model(results: Iterable[VectorResult], meta: Meta) -> dict[str, Any]:
    groups: dict[str, dict[str, Any]] = {}
    totals = _Counts()
    total_time = 0
    for res in results:
        if meta.omit_skipped and res.outcome == "skipped":
            continue
        g = groups.get(res.group)
        if g is None:
            g = {"name": res.group, "counts": _Counts(), "vectors": []}
            groups[res.group] = g
        g["counts"].add(res)
        totals.add(res)
        total_time += res.duration
        g["vectors"].append(vector_view(res))

    first_fail = ""
    group_views = []
    for name in sorted(groups):
        g = groups[name]
        vectors = sorted(g["vectors"], key=lambda v: v["id"])
        if first_fail == "":
            f = next((v for v in vectors if v["badge"] == "fail"), None)
            if f is not None:
                first_fail = f["id"]
        c: _Counts = g["counts"]
        group_views.append(
            {
                "name": name,
                "pctClass": c.pct_class(),
                "barWidth": c.bar_width(),
                "open": c.fail + c.errors > 0,
                "counts": c.view(),
                "vectors": vectors,
            }
        )

    generated = format_generated(meta.generated_at) if meta.generated_at is not None else ""
    properties = [{"name": k, "value": meta.properties[k]} for k in sorted(meta.properties)]
    return {
        "target": meta.target,
        "hasTarget": meta.target != "",
        "corpusVersion": meta.corpus_version,
        "hasCorpusVersion": meta.corpus_version != "",
        "generated": generated,
        "hasGenerated": generated != "",
        "totalTime": human_duration(total_time),
        "properties": properties,
        "hasProvenance": meta.target != "" or meta.corpus_version != "" or generated != "" or len(properties) > 0,
        "totals": totals.view(),
        "firstFail": first_fail,
        "groups": group_views,
    }


# Raw-file location of the corpus vector files; a text-fragment URL on it
# opens the file scrolled to the vector's "id" line.
DEFINITION_BASE = "https://raw.githubusercontent.com/cloud-portable/s3vectors/main/vectors/"


def definition_url(group: str, id: str) -> str:
    """Link to the vector's definition in the corpus repository. Group and id
    are plain concatenated, not percent-encoded: the corpus schema restricts
    both to [a-z0-9-], and avoiding an encoder keeps every reporter
    byte-identical. The fragment prefix is the pre-encoded form of `"id": "`."""
    return f"{DEFINITION_BASE}{group}.json#:~:text=%22id%22%3A%20%22{id}%22"


def vector_view(res: VectorResult) -> dict[str, Any]:
    badge = res.outcome
    reason = summary = detail = ""
    if res.outcome == "fail":
        summary, detail = failure_detail(res)
        if res.runner_error:
            badge = "error"
            summary = "runner error: " + res.runner_error
            if detail == res.runner_error:
                detail = ""  # nothing beyond what the message already says
    elif res.outcome == "blocked":
        reason = "blocked: " + res.reason
    elif res.outcome == "skipped":
        reason = "skipped: " + res.reason
    tags = list(res.tags)
    warnings = list(res.warnings)
    return {
        "id": res.id,
        "badge": badge,
        "duration": vector_duration(res.duration),
        "title": res.title,
        "hasTitle": res.title != "",
        "tags": tags,
        "hasTags": len(tags) > 0,
        "reason": reason,
        "hasReason": reason != "",
        "summary": summary,
        "hasSummary": summary != "",
        "detail": detail,
        "hasDetail": detail != "",
        "warnings": warnings,
        "source": res.source,
        "hasSource": res.source != "",
        "definitionURL": definition_url(res.group, res.id),
        "hasDesc": res.title != "" or len(tags) > 0,
        "hasOutcome": reason != "" or summary != "" or detail != "" or len(warnings) > 0,
    }


def failure_detail(res: VectorResult) -> tuple[str, str]:
    """Summarize the failing (last executed) step. The summary names only the
    step — the mismatches live solely in the detail block."""
    if not res.steps:
        return res.reason, ""
    step = res.steps[-1]
    summary = f"step {step.index + 1} ({step.name}) failed"
    lines = []
    if step.err:
        lines.append(step.err)
    for f in step.failures:
        lines.append(f"{f.field}: expected {f.expected}, got {f.actual}")
    return summary, "\n".join(lines)


def vector_duration(ns: int) -> str:
    """Seconds with exactly three decimals from whole ms (round half up)."""
    return secs3(ns) + "s"


def human_duration(ns: int) -> str:
    """"42.3s" under a minute; Go's Duration.String() form above, rounded to
    whole seconds ("4m12s", "1h0m0s")."""
    if ns < 60_000_000_000:
        return secs1(ns) + "s"
    secs = (ns + 500_000_000) // 1_000_000_000
    h, secs = divmod(secs, 3600)
    m, secs = divmod(secs, 60)
    return f"{h}h{m}m{secs}s" if h > 0 else f"{m}m{secs}s"


def format_generated(d) -> str:
    d = utc(d)
    return f"{d.year:04d}-{d.month:02d}-{d.day:02d} {d.hour:02d}:{d.minute:02d}:{d.second:02d} UTC"
