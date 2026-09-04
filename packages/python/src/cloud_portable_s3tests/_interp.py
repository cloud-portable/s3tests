"""Vector placeholder interpolation: ``${env.*}``, ``${res.<handle>.<attr>}``,
``${cap.<name>}`` and ``${data.<name>.<field>}``, with ``$${`` escaping. An
unresolvable placeholder is a vector-definition error."""

from __future__ import annotations

import json
from typing import Any, Callable


class InterpError(ValueError):
    """An unresolvable or malformed placeholder."""


class Scope:
    """The values placeholders resolve against while a vector runs.

    ``data`` is a resolver ``(name, field) -> str`` (see ``_vdata``).
    """

    def __init__(
        self,
        *,
        env: dict[str, str] | None = None,
        res: dict[str, dict[str, str]] | None = None,
        cap: dict[str, str] | None = None,
        data: Callable[[str, str], str] | None = None,
    ) -> None:
        self.env: dict[str, str] = env if env is not None else {}
        self.res: dict[str, dict[str, str]] = res if res is not None else {}
        self.cap: dict[str, str] = cap if cap is not None else {}
        self.data = data

    def string(self, s: str) -> str:
        """Interpolate every placeholder in ``s``, or raise on the first
        unresolvable one (the raw text must never be sent)."""
        if "$" not in s:
            return s
        out: list[str] = []
        i = 0
        n = len(s)
        while i < n:
            c = s[i]
            if c != "$":
                out.append(c)
                i += 1
                continue
            if s.startswith("$${", i):
                out.append("${")
                i += 3
                continue
            if s.startswith("${", i):
                end = s.find("}", i)
                if end < 0:
                    raise InterpError(f"unterminated placeholder in {json.dumps(s)}")
                out.append(self._resolve(s[i + 2 : end]))
                i = end + 1
                continue
            # Any other $ is literal as-is.
            out.append(c)
            i += 1
        return "".join(out)

    def _resolve(self, expr: str) -> str:
        dot = expr.find(".")
        if dot < 0:
            raise InterpError(f"unresolvable placeholder ${{{expr}}}: missing path")
        ns = expr[:dot]
        path = expr[dot + 1 :]
        if ns == "env":
            if path in self.env:
                return self.env[path]
        elif ns == "res":
            d = path.find(".")
            if d < 0:
                raise InterpError(f"unresolvable placeholder ${{{expr}}}: want res.<handle>.<attr>")
            handle, attr = path[:d], path[d + 1 :]
            attrs = self.res.get(handle)
            if attrs is not None:
                if attr in attrs:
                    return attrs[attr]
                raise InterpError(
                    f"unresolvable placeholder ${{{expr}}}: resource {json.dumps(handle)} "
                    f"has no attribute {json.dumps(attr)}"
                )
        elif ns == "cap":
            if path in self.cap:
                return self.cap[path]
        elif ns == "data":
            d = path.find(".")
            if d < 0:
                raise InterpError(f"unresolvable placeholder ${{{expr}}}: want data.<name>.<field>")
            if self.data is not None:
                try:
                    return self.data(path[:d], path[d + 1 :])
                except Exception as err:  # noqa: BLE001 - any resolver failure is a vector error
                    raise InterpError(f"unresolvable placeholder ${{{expr}}}: {err}") from err
        else:
            raise InterpError(f"unresolvable placeholder ${{{expr}}}: unknown namespace {json.dumps(ns)}")
        raise InterpError(f"unresolvable placeholder ${{{expr}}}")

    def value(self, v: Any) -> Any:
        """Interpolate every string inside a decoded JSON value, returning a
        new value; the input (typically shared corpus data) is never mutated.
        Object keys are not interpolated."""
        if isinstance(v, str):
            return self.string(v)
        if isinstance(v, list):
            return [self.value(e) for e in v]
        if isinstance(v, dict):
            return {k: self.value(e) for k, e in v.items()}
        return v
