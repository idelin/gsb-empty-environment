"""Render errors as minimal, explainable constraint chains with positions."""
from __future__ import annotations

from .errors import (InfiniteType, LexError, ParseError, RuntimeFailure,
                     TypeMismatch, UnboundName)
from .types import prune, show
from .unify import find_path


def render_error(err, src) -> str:
    if isinstance(err, TypeMismatch):
        return _render_mismatch(err)
    if isinstance(err, InfiniteType):
        return _render_infinite(err)
    if isinstance(err, (LexError, ParseError)):
        return f"error: {err.message} ({err.span})"
    if isinstance(err, UnboundName):
        return f"error: unbound variable `{err.name}` ({err.span})"
    if isinstance(err, RuntimeFailure):
        loc = f" ({err.span})" if err.span else ""
        return f"runtime error: {err.message}{loc}"
    return f"error: {err}"


def _describe(t) -> str:
    t = prune(t)
    if t.origin is not None:
        return f"`{show(t)}` — {t.origin.message} ({t.origin.span})"
    return f"`{show(t)}` — (no source location)"


def _render_mismatch(err: TypeMismatch) -> str:
    left = prune(err.left)
    right = prune(err.right)
    lines = [
        f"error: type mismatch — cannot unify `{show(left)}` with `{show(right)}`",
        f"  forced by: {err.reason.message} ({err.reason.span})",
        "  constraint chain:",
    ]
    path = find_path(left, right)
    if path is None:
        lines.append(f"    {_describe(left)}")
        lines.append(f"    {_describe(right)}")
    else:
        lines.append(f"    {_describe(path[0])}")
        i = 1
        while i < len(path):
            reason = path[i]
            node = path[i + 1]
            lines.append(f"      | {reason.message} ({reason.span})")
            lines.append(f"    {_describe(node)}")
            i += 2
    return "\n".join(lines)


def _render_infinite(err: InfiniteType) -> str:
    lines = [
        "error: cannot construct an infinite type (occurs check failed)",
        f"  {_describe(err.var)}",
        f"  occurs in its own type `{show(err.typ)}`",
        f"  forced by: {err.reason.message} ({err.reason.span})",
    ]
    return "\n".join(lines)
