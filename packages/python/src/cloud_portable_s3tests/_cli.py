"""The s3tests CLI: run the corpus against an endpoint, stream console
progress, and write file reports for each --report/-r flag.

By default results just stream to the console. Reports are written for each
--report (-r) flag, given as <format> (default path: report.xml for junit,
report.html for html) or <format>=<path>, repeatable.

Vectors are selected with --groups/--tags/--ids and dropped with the matching
--exclude-* flags; the --skip-* flags instead keep matching vectors in the
results as "skipped" (with the flag as the reason) without running them, so
reports document a skip-list rather than silently omitting it.

The exit code is 1 when any vector failed (including runner errors) and 0
otherwise; blocked and skipped vectors do not affect it (a missing second
identity blocks the $credential vectors by design — supply
--alt-access-key/--alt-secret-key to run them).
"""

from __future__ import annotations

import argparse
import json
import os
import signal
import sys
import threading
import time
from datetime import datetime, timezone
from typing import IO, Callable

from . import apply_filters, exclude_groups, exclude_ids, exclude_tags, groups, ids, skip, tags, vectors
from ._config import Config, Credential
from ._result import VectorResult
from ._runner import Runner
from ._timefmt import pct1, secs1, secs2
from .report import Meta, html, junit

REPORTERS: dict[str, tuple[Callable, str]] = {
    "junit": (junit.write, "report.xml"),
    "html": (html.write, "report.html"),
}

USAGE = f"""usage: s3tests --endpoint <url> --access-key <id> --secret-key <key> [options]

connection:
  --endpoint <url>          S3 endpoint under test (env S3TESTS_ENDPOINT)
  --access-key <id>         access key id (env S3TESTS_ACCESS_KEY)
  --secret-key <key>        secret access key (env S3TESTS_SECRET_KEY)
  --region <region>         SigV4 region (default us-east-1)
  --virtual-host            virtual-hosted-style addressing (default path-style)
  --concurrency <n>         vectors executed in parallel (default 1)
  --keep-resources          do not tear down resources, leaving buckets for debugging
  --alt-access-key <id>     second identity for $credential vectors (env S3TESTS_ALT_ACCESS_KEY)
  --alt-secret-key <key>    second identity secret key (env S3TESTS_ALT_SECRET_KEY)
  --alt-canonical-id <id>   second identity canonical id (for ACL vectors)
  --alt-display-name <name> second identity display name

selection (comma-separated):
  --groups, --tags, --ids                          vectors to run (empty = all)
  --exclude-groups, --exclude-tags, --exclude-ids  drop from the run (absent from results)
  --skip-groups, --skip-tags, --skip-ids           skip: not run, but recorded as skipped in results

reporting:
  -r, --report <format>[=<path>]  write a report (formats: {", ".join(sorted(REPORTERS))};
                                  default paths report.xml, report.html); repeatable
  --target <name>                 target name stamped into reports (defaults to the endpoint)
  --quiet                         suppress per-vector progress output"""


class _UsageError(Exception):
    pass


class _Parser(argparse.ArgumentParser):
    def error(self, message: str) -> None:  # type: ignore[override]
        raise _UsageError(message)


def _build_parser() -> _Parser:
    p = _Parser(prog="s3tests", add_help=False, allow_abbrev=False)
    for name in (
        "endpoint", "access-key", "secret-key", "alt-access-key", "alt-secret-key", "alt-canonical-id",
        "alt-display-name", "groups", "tags", "ids", "exclude-groups", "exclude-tags", "exclude-ids",
        "skip-groups", "skip-tags", "skip-ids", "target",
    ):
        p.add_argument(f"--{name}")
    p.add_argument("--region", default="us-east-1")
    p.add_argument("--virtual-host", action="store_true")
    p.add_argument("--concurrency", default="1")
    p.add_argument("--keep-resources", action="store_true")
    p.add_argument("--report", "-r", action="append", default=[])
    p.add_argument("--quiet", action="store_true")
    p.add_argument("--help", "-h", action="store_true")
    return p


def run(argv: list[str], stdout: IO[str], stderr: IO[str]) -> int:
    """Run the CLI; returns the process exit code."""
    try:
        ns, rest = _build_parser().parse_known_args(argv)
        if rest:
            raise _UsageError(f"unrecognized arguments: {' '.join(rest)}")
    except _UsageError as err:
        stderr.write(f"error: {err}\n{USAGE}\n")
        return 2
    if ns.help:
        stdout.write(USAGE + "\n")
        return 0
    values = vars(ns)

    def env(name: str) -> str:
        return os.environ.get(name, "")

    endpoint = values["endpoint"] or env("S3TESTS_ENDPOINT")
    access_key = values["access_key"] or env("S3TESTS_ACCESS_KEY")
    secret_key = values["secret_key"] or env("S3TESTS_SECRET_KEY")
    if not endpoint or not access_key or not secret_key:
        stderr.write(f"error: --endpoint, --access-key and --secret-key are required\n{USAGE}\n")
        return 2

    try:
        reports = [_parse_report_spec(v) for v in values["report"]]
    except _UsageError as err:
        stderr.write(f"error: {err}\n")
        return 2

    try:
        concurrency = int(values["concurrency"])
    except ValueError:
        concurrency = 1
    config = Config(
        endpoint=endpoint,
        region=values["region"],
        credentials=Credential(access_key, secret_key),
        virtual_host_style=values["virtual_host"],
        concurrency=concurrency or 1,
        keep_resources=values["keep_resources"],
    )
    alt_access = values["alt_access_key"] or env("S3TESTS_ALT_ACCESS_KEY")
    alt_secret = values["alt_secret_key"] or env("S3TESTS_ALT_SECRET_KEY")
    if alt_access and alt_secret:
        cred = Credential(
            alt_access, alt_secret,
            canonical_id=values["alt_canonical_id"] or env("S3TESTS_ALT_CANONICAL_ID"),
            display_name=values["alt_display_name"] or env("S3TESTS_ALT_DISPLAY_NAME"),
        )
        config.provision_credential = lambda handle: cred

    try:
        runner = Runner(config)
    except ValueError as err:
        stderr.write(f"error: {err}\n")
        return 2

    filters, properties = _build_filters(values)
    selected = apply_filters(vectors(), *filters)
    if not selected:
        stderr.write("error: no vectors selected\n")
        return 2
    skips = _build_skips(values, properties)

    # Ctrl-C cancels the run; in-flight vectors still tear their buckets down.
    # A second interrupt hard-exits.
    cancel = threading.Event()
    interrupted = [False]

    def on_sigint(signum, frame):
        if interrupted[0]:
            os._exit(130)
        interrupted[0] = True
        stderr.write("\ninterrupted — cancelling (Ctrl-C again to force quit)\n")
        cancel.set()

    prev_handler = None
    on_main_thread = threading.current_thread() is threading.main_thread()
    if on_main_thread:
        prev_handler = signal.signal(signal.SIGINT, on_sigint)

    color = _colors_enabled(stdout)
    counts = {"pass": 0, "fail": 0, "blocked": 0, "skipped": 0}
    runner_errs = 0
    results: list[VectorResult] = []
    started = time.perf_counter_ns()
    try:
        for res in runner.run(selected, skip=skips, cancel=cancel):
            results.append(res)
            counts[res.outcome] += 1
            if res.runner_error:
                runner_errs += 1
            if not values["quiet"]:
                stdout.write(progress_line(res, color) + "\n")
    finally:
        if on_main_thread and prev_handler is not None:
            signal.signal(signal.SIGINT, prev_handler)
    elapsed = time.perf_counter_ns() - started

    meta = Meta(
        corpus_version=runner.corpus_version(),
        target=values["target"] or endpoint,
        properties=properties,
        generated_at=datetime.now(timezone.utc),
    )
    reporter_failed = False
    for fmt, path in reports:
        try:
            with open(path, "wb") as f:
                REPORTERS[fmt][0](f, results, meta)
            stdout.write(f"wrote {fmt} report {path}\n")
        except OSError as err:
            stderr.write(f"error: writing {fmt} report: {err}\n")
            reporter_failed = True

    attempted = len(results) - counts["skipped"]
    pct = pct1(counts["pass"], attempted) if attempted > 0 else "—"
    stdout.write(
        f"\n{len(results)} vectors: {counts['pass']} pass, {counts['fail']} fail ({runner_errs} runner errors), "
        f"{counts['blocked']} blocked, {counts['skipped']} skipped — {pct} pass rate in {secs1(elapsed)}s "
        f"(corpus {runner.corpus_version()})\n"
    )

    if counts["fail"] > 0 or reporter_failed:
        return 1
    if interrupted[0]:
        return 130
    return 0


def _parse_report_spec(value: str) -> tuple[str, str]:
    eq = value.find("=")
    fmt = value if eq < 0 else value[:eq]
    path = "" if eq < 0 else value[eq + 1 :]
    if fmt == "" or (eq >= 0 and path == ""):
        raise _UsageError(f"expected <format> or <format>=<path>, got {json.dumps(value)}")
    if fmt not in REPORTERS:
        raise _UsageError(f"unknown report format {json.dumps(fmt)} (formats: {', '.join(sorted(REPORTERS))})")
    return fmt, path or REPORTERS[fmt][1]


def _build_filters(values: dict) -> tuple[list, dict[str, str]]:
    """The selection flags as filter funcs, plus the properties stamped into
    reports so filtered runs self-describe."""
    filters = []
    properties: dict[str, str] = {}
    for name, ctor in (
        ("groups", groups), ("tags", tags), ("ids", ids),
        ("exclude-groups", exclude_groups), ("exclude-tags", exclude_tags), ("exclude-ids", exclude_ids),
    ):
        val = values[name.replace("-", "_")]
        if val:
            filters.append(ctor(*val.split(",")))
            properties[name] = val
    return filters, properties


def _build_skips(values: dict, properties: dict[str, str]) -> list:
    """The --skip-* flags as skip rules for run(). Unlike the exclude filters,
    skipped vectors stay in the results (and reports) with the flag that
    skipped them as the reason; the flag values are also stamped into
    properties."""
    rules = []
    for name, ctor in (("skip-groups", groups), ("skip-tags", tags), ("skip-ids", ids)):
        val = values[name.replace("-", "_")]
        if val:
            rules.append(skip(f"skipped by --{name}", ctor(*val.split(","))))
            properties[name] = val
    return rules


_ANSI = {"reset": "\x1b[0m", "green": "\x1b[32m", "red": "\x1b[31m", "amber": "\x1b[33m", "violet": "\x1b[35m", "dim": "\x1b[2m"}


def _colors_enabled(stdout: IO[str]) -> bool:
    if os.environ.get("NO_COLOR"):
        return False
    isatty = getattr(stdout, "isatty", None)
    return bool(isatty and isatty())


def progress_line(res: VectorResult, color: bool) -> str:
    outcome = res.outcome
    tint = ""
    detail = ""
    if res.outcome == "pass":
        tint = _ANSI["green"]
    elif res.outcome == "fail":
        tint = _ANSI["red"]
        if res.runner_error:
            outcome = "error"
            tint = _ANSI["violet"]
            detail = f" — runner error: {res.runner_error}"
        elif res.steps:
            step = res.steps[-1]
            detail = f" — step {step.index + 1} ({step.name}) failed"
            if step.failures:
                f = step.failures[0]
                detail += f": {f.field}: expected {f.expected}, got {f.actual}"
            elif step.err:
                detail += f": {step.err}"
    elif res.outcome == "blocked":
        tint = _ANSI["amber"]
        detail = f" — {res.reason}"
    elif res.outcome == "skipped":
        tint = _ANSI["dim"]
        detail = f" — {res.reason}"
    width = 8
    if color and tint:
        outcome = tint + outcome + _ANSI["reset"]
        width += len(tint) + len(_ANSI["reset"])
    return f"{outcome.rjust(width)} {res.id} ({secs2(res.duration)}s){detail}"


def main() -> None:
    sys.exit(run(sys.argv[1:], sys.stdout, sys.stderr))
