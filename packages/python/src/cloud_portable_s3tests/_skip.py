"""Skip rules for ``Runner.run()``: record vectors as ``skipped`` (with a
reason) instead of executing them. Unlike dropping vectors with
``apply_filters`` beforehand, skipped vectors still appear in the result
stream — outcome 'skipped', the reason, no steps, zero duration — so reports
stay comparable across runs and document what was deliberately not exercised.

A skip rule is any callable ``(vector) -> str | None``: a string (even an
empty one) is the reason to skip the vector; None lets it run. ``skip()``
builds one from a reason and filters; a hand-written rule is the general form
for reasons that vary per vector::

    runner.run(selected, skip=[
        skip("known server bug #123", ids("multipart-0013")),
        lambda v: known_issues.get(v["id"]),  # id -> issue link, or None
    ])
"""

from __future__ import annotations

from typing import Callable, Iterable, Optional

from ._filter import FilterFunc, Vector

SkipFunc = Callable[[Vector], Optional[str]]


def skip(reason: str, *filters: FilterFunc) -> SkipFunc:
    """A rule skipping vectors matched by every given filter (logical AND,
    exactly as apply_filters selects) with the given reason. With no filters
    every vector is skipped (a dry run that lists the selection). Several
    rules compose: the first one matching a vector supplies its reason."""
    return lambda v: reason if all(f(v) for f in filters) else None


def skip_reason(rules: Iterable[SkipFunc], vector: Vector) -> Optional[str]:
    """The reason the first matching rule gives for skipping the vector, or
    None when no rule matches."""
    for rule in rules:
        reason = rule(vector)
        if isinstance(reason, str):
            return reason
    return None
