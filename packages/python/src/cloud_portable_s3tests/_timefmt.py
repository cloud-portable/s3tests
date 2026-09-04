"""Integer-arithmetic number formatting shared by the reporters and the CLI.

Every rendered duration or percentage is derived from integer nanoseconds
with half-up rounding, matching the Go and JS runners byte for byte. Python's
``round()`` (half-even) and float formatting (``f"{0.0625:.3f}"`` is ``0.062``
where JS ``toFixed`` gives ``0.063``) must not be used for report output.
"""

from __future__ import annotations

from datetime import datetime, timezone


def ms_half_up(ns: int) -> int:
    """Whole milliseconds, rounding half up."""
    return (ns + 500_000) // 1_000_000


def secs3(ns: int) -> str:
    """Seconds with exactly three decimals from whole milliseconds ("1.234")."""
    ms = ms_half_up(ns)
    return f"{ms // 1000}.{ms % 1000:03d}"


def secs2(ns: int) -> str:
    """Seconds with two decimals ("0.00")."""
    cs = (ns + 5_000_000) // 10_000_000
    return f"{cs // 100}.{cs % 100:02d}"


def secs1(ns: int) -> str:
    """Seconds with one decimal ("42.3")."""
    ds = (ns + 50_000_000) // 100_000_000
    return f"{ds // 10}.{ds % 10}"


def pct1(num: int, den: int) -> str:
    """``num/den`` as a percentage with one decimal ("6.3%"); den must be > 0."""
    p10 = (1000 * num + den // 2) // den
    return f"{p10 // 10}.{p10 % 10}%"


def utc(d: datetime) -> datetime:
    """The datetime in UTC; a naive datetime is taken to already be UTC."""
    if d.tzinfo is None:
        return d.replace(tzinfo=timezone.utc)
    return d.astimezone(timezone.utc)
