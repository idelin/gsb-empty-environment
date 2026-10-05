"""Readable/writable subtree policy.

A policy is a set of *readable* (input) and *writable* (output) directory
subtrees, expressed as root-relative paths.  Membership is decided on the
**resolved logical path** (after symlink and ``..`` normalization), matched
component-wise, so a sibling directory that merely shares a string prefix
(``input/`` vs ``in/``) is never confused with a permitted subtree.
"""

from __future__ import annotations

from .errors import PolicyDenied


def _norm(subtree: str) -> tuple[str, ...]:
    if subtree.startswith("/"):
        raise ValueError(f"policy subtree must be relative: {subtree!r}")
    parts = tuple(c for c in subtree.split("/") if c not in ("", "."))
    if any(c == ".." for c in parts):
        raise ValueError(f"policy subtree must not contain '..': {subtree!r}")
    return parts


class Policy:
    """Immutable set of readable and writable subtrees.

    ``""`` (the empty subtree) denotes the whole root.  A path is *under* a
    subtree when the subtree is a component-wise prefix of it.
    """

    __slots__ = ("readable", "writable")

    def __init__(self, readable=(), writable=()):
        self.readable: tuple[tuple[str, ...], ...] = tuple(_norm(p) for p in readable)
        self.writable: tuple[tuple[str, ...], ...] = tuple(_norm(p) for p in writable)

    @staticmethod
    def _under(parts: tuple[str, ...], subtree: tuple[str, ...]) -> bool:
        return len(parts) >= len(subtree) and parts[: len(subtree)] == subtree

    def can_read(self, parts: tuple[str, ...]) -> bool:
        return any(self._under(parts, s) for s in self.readable)

    def can_write(self, parts: tuple[str, ...]) -> bool:
        return any(self._under(parts, s) for s in self.writable)

    def require_read(self, parts: tuple[str, ...]) -> None:
        if not self.can_read(parts):
            raise PolicyDenied(
                f"read of '/{'/'.join(parts)}' denied: not under any readable subtree"
            )

    def require_write(self, parts: tuple[str, ...]) -> None:
        if not self.can_write(parts):
            raise PolicyDenied(
                f"write of '/{'/'.join(parts)}' denied: not under any writable subtree"
            )
