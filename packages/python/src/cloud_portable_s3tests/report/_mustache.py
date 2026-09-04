"""A minimal logic-less mustache renderer for the shared report template.

The shared template (``page.mustache``) uses escaped variables, ``#``/``^``
sections, dotted names and ``{{.}}`` only. Rendering this subset directly, with
spec-compliant standalone-line handling, lets the reporter escape exactly the
entity set Go's ``template.HTMLEscapeString`` uses (``& ' < > "``), which no
published Python mustache library allows without monkeypatching. The shared
golden test pins the output byte for byte against the Go renderer.
"""

from __future__ import annotations

import re
from typing import Any, Callable

_TAG = re.compile(r"\{\{(\{?)\s*([#^/!&]?)\s*([^}]*?)\s*\}?\}\}")


def go_escape(s: str) -> str:
    """Exactly Go's template.HTMLEscapeString."""
    return (
        s.replace("&", "&amp;")
        .replace("'", "&#39;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&#34;")
    )


# A parsed template is a list of nodes:
#   ("text", str) | ("var", name, escaped) | ("section", name, inverted, children)
Node = tuple


class Template:
    def __init__(self, source: str, escape: Callable[[str], str] = go_escape) -> None:
        self._escape = escape
        self._nodes = _parse(_tokenize(source))

    def render(self, view: Any) -> str:
        out: list[str] = []
        _render(self._nodes, [view], out, self._escape)
        return "".join(out)


def _tokenize(src: str) -> list[tuple]:
    """Split the template into ("text", s) and ("tag", sigil, name, unescaped)
    tokens, removing standalone section/comment lines per the spec."""
    tokens: list[tuple] = []
    pos = 0
    for m in _TAG.finditer(src):
        text = src[pos : m.start()]
        triple, sigil, name = m.group(1), m.group(2), m.group(3)
        unescaped = triple == "{" or sigil == "&"
        tag = ("tag", sigil if sigil != "&" else "", name, unescaped)
        pos = m.end()
        if sigil in ("#", "^", "/", "!"):
            # Standalone check (on the source, so other tags on the same line
            # disqualify): only whitespace before the tag on its line, and
            # only whitespace up to the newline (or EOF) after it.
            src_line_start = src.rfind("\n", 0, m.start()) + 1
            before = src[src_line_start : m.start()]
            after_m = re.match(r"[ \t]*(\r?\n|$)", src[pos:])
            if before.strip(" \t") == "" and after_m is not None:
                text = text[: text.rfind("\n") + 1]
                pos += after_m.end()
        if text:
            tokens.append(("text", text))
        tokens.append(tag)
    if pos < len(src):
        tokens.append(("text", src[pos:]))
    return tokens


def _parse(tokens: list[tuple]) -> list[Node]:
    root: list[Node] = []
    stack: list[tuple[str, list[Node]]] = [("", root)]
    for tok in tokens:
        if tok[0] == "text":
            stack[-1][1].append(("text", tok[1]))
            continue
        _, sigil, name, unescaped = tok
        if sigil == "!":
            continue
        if sigil in ("#", "^"):
            children: list[Node] = []
            stack[-1][1].append(("section", name, sigil == "^", children))
            stack.append((name, children))
        elif sigil == "/":
            open_name, _ = stack.pop()
            if open_name != name or not stack:
                raise ValueError(f"mustache: unexpected closing tag {{{{/{name}}}}}")
        else:
            stack[-1][1].append(("var", name, not unescaped))
    if len(stack) != 1:
        raise ValueError(f"mustache: unclosed section {{{{#{stack[-1][0]}}}}}")
    return root


_MISSING = object()


def _lookup(name: str, stack: list[Any]) -> Any:
    if name == ".":
        return stack[-1]
    parts = name.split(".")
    # The first segment is resolved against the context stack, innermost
    # first; the rest are resolved on the value found.
    value: Any = _MISSING
    for ctx in reversed(stack):
        v = _get(ctx, parts[0])
        if v is not _MISSING:
            value = v
            break
    if value is _MISSING:
        return _MISSING
    for p in parts[1:]:
        value = _get(value, p)
        if value is _MISSING:
            return _MISSING
    return value


def _get(ctx: Any, key: str) -> Any:
    if isinstance(ctx, dict):
        return ctx[key] if key in ctx else _MISSING
    return _MISSING


def _truthy(v: Any) -> bool:
    if v is _MISSING or v is None or v is False:
        return False
    if isinstance(v, (list, tuple, str)) and len(v) == 0:
        return False
    if isinstance(v, (int, float)) and not isinstance(v, bool) and v == 0:
        return False
    return True


def _stringify(v: Any) -> str:
    if v is _MISSING or v is None:
        return ""
    if isinstance(v, bool):
        return "true" if v else "false"
    return str(v)


def _render(nodes: list[Node], stack: list[Any], out: list[str], escape: Callable[[str], str]) -> None:
    for node in nodes:
        kind = node[0]
        if kind == "text":
            out.append(node[1])
        elif kind == "var":
            s = _stringify(_lookup(node[1], stack))
            out.append(escape(s) if node[2] else s)
        else:
            _, name, inverted, children = node
            value = _lookup(name, stack)
            if inverted:
                if not _truthy(value):
                    _render(children, stack, out, escape)
                continue
            if not _truthy(value):
                continue
            if isinstance(value, (list, tuple)):
                for item in value:
                    stack.append(item)
                    _render(children, stack, out, escape)
                    stack.pop()
            else:
                stack.append(value)
                _render(children, stack, out, escape)
                stack.pop()
