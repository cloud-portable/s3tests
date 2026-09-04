"""JUnit XML reporter, mapping per the corpus reporting guide (identical to
the Go and JS runners'): one <testcase> per vector (name = id, classname =
group), one <testsuite> per group; blocked -> <skipped message="blocked:
...">, never <failure>; runner-error fails -> <error>; warnings ->
<system-out>. Output is deterministic; a testsuite timestamp appears only
when meta.generated_at is set."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import BinaryIO, Iterable

from .._result import VectorResult
from .._timefmt import secs3, utc
from . import Meta


def write(w: BinaryIO, results: Iterable[VectorResult], meta: Meta | None = None) -> None:
    """Consume the results and write a JUnit XML document (UTF-8) to ``w``."""
    w.write(render(results, meta).encode("utf-8"))


@dataclass
class _Suite:
    name: str
    tests: int = 0
    failures: int = 0
    errors: int = 0
    skipped: int = 0
    time: int = 0
    cases: list[VectorResult] = field(default_factory=list)


def render(results: Iterable[VectorResult], meta: Meta | None = None) -> str:
    meta = meta or Meta()
    suites: dict[str, _Suite] = {}
    totals = _Suite("s3vectors")
    for res in results:
        if meta.omit_skipped and res.outcome == "skipped":
            continue
        suite = suites.get(res.group)
        if suite is None:
            suite = _Suite(res.group)
            suites[res.group] = suite
        suite.cases.append(res)
        for s in (suite, totals):
            s.tests += 1
            if res.outcome == "fail":
                if res.runner_error:
                    s.errors += 1
                else:
                    s.failures += 1
            elif res.outcome in ("blocked", "skipped"):
                s.skipped += 1
            s.time += res.duration

    out = ['<?xml version="1.0" encoding="UTF-8"?>']
    out.append(
        f'<testsuites name="s3vectors" tests="{totals.tests}" failures="{totals.failures}" '
        f'errors="{totals.errors}" skipped="{totals.skipped}" time="{secs3(totals.time)}">'
    )
    ts = _timestamp(meta)
    props = _suite_properties(meta)
    for suite in suites.values():
        out.append(
            f'  <testsuite name="{_attr(suite.name)}" tests="{suite.tests}" failures="{suite.failures}" '
            f'errors="{suite.errors}" skipped="{suite.skipped}" time="{secs3(suite.time)}"'
            + (f' timestamp="{ts}"' if ts else "")
            + ">"
        )
        if props:
            out.append("    <properties>")
            for name, value in props:
                out.append(f'      <property name="{_attr(name)}" value="{_attr(value)}"></property>')
            out.append("    </properties>")
        for res in suite.cases:
            out.extend(_testcase(res))
        out.append("  </testsuite>")
    out.append("</testsuites>")
    return "\n".join(out) + "\n"


def _testcase(res: VectorResult) -> list[str]:
    lines = [f'    <testcase name="{_attr(res.id)}" classname="{_attr(res.group)}" time="{secs3(res.duration)}">']
    if res.tags:
        lines.append("      <properties>")
        lines.append(f'        <property name="tags" value="{_attr(",".join(res.tags))}"></property>')
        lines.append("      </properties>")
    if res.outcome == "fail":
        msg, body = failure_detail(res)
        if res.runner_error:
            lines.append(f'      <error message="{_attr("runner error: " + res.runner_error)}">{_text(body)}</error>')
        else:
            lines.append(f'      <failure message="{_attr(msg)}">{_text(body)}</failure>')
    elif res.outcome == "blocked":
        lines.append(f'      <skipped message="{_attr("blocked: " + res.reason)}"></skipped>')
    elif res.outcome == "skipped":
        lines.append(f'      <skipped message="{_attr("skipped: " + res.reason)}"></skipped>')
    if res.warnings:
        lines.append(f'      <system-out>{_text(chr(10).join(res.warnings))}</system-out>')
    lines.append("    </testcase>")
    return lines


def failure_detail(res: VectorResult) -> tuple[str, str]:
    """Summarize the failing (last executed) step: a one-line message (step
    reference + first mismatch) plus the full detail as text."""
    if not res.steps:
        return res.reason, ""
    step = res.steps[-1]
    prefix = f"step {step.index + 1} ({step.name})"
    lines = []
    if step.err:
        lines.append(step.err)
    for f in step.failures:
        lines.append(f"{f.field}: expected {f.expected}, got {f.actual}")
    if not lines:
        return prefix + ": failed", ""
    return prefix + ": " + lines[0], "\n".join(lines)


def _suite_properties(meta: Meta) -> list[tuple[str, str]]:
    props: list[tuple[str, str]] = []
    if meta.corpus_version:
        props.append(("corpusVersion", meta.corpus_version))
    if meta.target:
        props.append(("target", meta.target))
    for k in sorted(meta.properties):
        props.append((k, meta.properties[k]))
    return props


def _timestamp(meta: Meta) -> str:
    if meta.generated_at is None:
        return ""
    d = utc(meta.generated_at)
    return f"{d.year:04d}-{d.month:02d}-{d.day:02d}T{d.hour:02d}:{d.minute:02d}:{d.second:02d}"


def _attr(s: str) -> str:
    return (
        str(s).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;").replace("'", "&apos;")
    )


def _text(s: str) -> str:
    return str(s).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
