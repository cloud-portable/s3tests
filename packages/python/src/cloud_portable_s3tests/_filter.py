"""Vector selection as composable functions. ``apply_filters`` ANDs its
filters; the exclude filters return False for matches, so ANDing drops them."""

from __future__ import annotations

from typing import Any, Callable, Iterable, Mapping

Vector = Mapping[str, Any]
FilterFunc = Callable[[Vector], bool]


def apply_filters(vectors: Iterable[Vector], *filters: FilterFunc) -> list[Vector]:
    """The vectors selected by every filter (logical AND), preserving order.
    With no filters every vector is returned (as a new list)."""
    vs = list(vectors)
    if not filters:
        return vs
    return [v for v in vs if all(f(v) for f in filters)]


def groups(*names: str) -> FilterFunc:
    """Vectors in any of the given feature groups."""
    return lambda v: v.get("group") in names


def tags(*wanted: str) -> FilterFunc:
    """Vectors carrying at least one of the given tags."""
    return lambda v: any(t in wanted for t in v.get("tags") or [])


def ids(*wanted: str) -> FilterFunc:
    """Vectors with any of the given ids."""
    return lambda v: v.get("id") in wanted


def exclude_groups(*names: str) -> FilterFunc:
    """Drop vectors in any of the given feature groups."""
    return lambda v: v.get("group") not in names


def exclude_tags(*wanted: str) -> FilterFunc:
    """Drop vectors carrying any of the given tags."""
    return lambda v: not any(t in wanted for t in v.get("tags") or [])


def exclude_ids(*wanted: str) -> FilterFunc:
    """Drop vectors with any of the given ids. Dropped vectors leave no trace
    in results; to keep them visible as skipped, pass skip rules to run()."""
    return lambda v: v.get("id") not in wanted
