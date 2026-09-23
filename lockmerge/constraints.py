"""Dependency constraint parsing and matching against exact versions.

Supported forms: ``*``, ``1.2.3``, ``>=1.2.0``, ``<2.0.0``, ``^1.2.3``
(compatible-update) and ``~1.2.3`` (same minor). Constraints may be
combined with commas, e.g. ``>=1.2.0,<2.0.0``.
"""

from __future__ import annotations

from .version import Version

_OPS = ("==", ">=", "<=", ">", "<", "=")


class Constraint:
    __slots__ = ("raw", "checks")

    def __init__(self, raw: str, checks: list[tuple[str, Version]]) -> None:
        self.raw = raw
        self.checks = checks

    @classmethod
    def parse(cls, text: str) -> "Constraint":
        raw = str(text).strip()
        if raw in ("", "*", "any"):
            return cls(raw, [])
        checks: list[tuple[str, Version]] = []
        for part in raw.split(","):
            clause = part.strip()
            if not clause:
                continue
            if clause in ("*", "any"):
                continue
            if clause[0].isdigit():
                op, value = "==", Version.parse(clause)
            else:
                op = next((o for o in _OPS if clause.startswith(o)), None)
                if op is None:
                    if clause.startswith("^"):
                        op, value = "^", Version.parse(clause[1:].strip())
                    elif clause.startswith("~"):
                        op, value = "~", Version.parse(clause[1:].strip())
                    else:
                        raise ValueError(f"unsupported constraint: {clause!r}")
                else:
                    value = Version.parse(clause[len(op):].strip())
            if op == "=":
                op = "=="
            if op in ("^", "~"):
                checks.extend(_expand_caret_tilde(op, value))
            else:
                checks.append((op, value))
        return cls(raw, checks)

    def satisfies(self, version: Version) -> bool:
        for op, target in self.checks:
            if not _compare(version, op, target):
                return False
        return True

    def __str__(self) -> str:
        return self.raw

    def __repr__(self) -> str:
        return f"Constraint({self.raw!r})"


def _compare(version: Version, op: str, target: Version) -> bool:
    if op == "==":
        return version == target
    if op == ">=":
        return version >= target
    if op == "<=":
        return version <= target
    if op == ">":
        return version > target
    if op == "<":
        return version < target
    raise ValueError(f"unknown operator {op!r}")


def _expand_caret_tilde(op: str, anchor: Version) -> list[tuple[str, Version]]:
    checks: list[tuple[str, Version]] = [(">=", anchor)]
    if op == "^":
        # ^0.x.y stays within 0.x; ^0.0.z pins z; otherwise same major.
        if anchor.major > 0:
            upper = Version(anchor.major + 1, 0, 0)
        elif anchor.minor > 0:
            upper = Version(0, anchor.minor + 1, 0)
        else:
            upper = Version(0, 0, anchor.patch + 1)
    else:
        upper = Version(anchor.major, anchor.minor + 1, 0)
    checks.append(("<", upper))
    return checks
