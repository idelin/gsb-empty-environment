"""Version constraints: comma-separated comparators, e.g. ">=1.0.0,<2.0.0"."""

from __future__ import annotations

from dataclasses import dataclass

from .version import Version

_OPS = (">=", "<=", "==", "!=", ">", "<")


class ConstraintError(ValueError):
    """Raised when a constraint expression cannot be parsed."""


@dataclass(frozen=True)
class Comparator:
    op: str
    version: Version

    def allows(self, version: Version) -> bool:
        if self.op == ">=":
            return version >= self.version
        if self.op == "<=":
            return version <= self.version
        if self.op == "==":
            return version == self.version
        if self.op == "!=":
            return version != self.version
        if self.op == ">":
            return version > self.version
        if self.op == "<":
            return version < self.version
        raise ConstraintError(f"unknown operator {self.op!r}")

    def __str__(self) -> str:
        return f"{self.op}{self.version}"


@dataclass(frozen=True)
class Constraint:
    comparators: tuple = ()

    @classmethod
    def parse(cls, text: str) -> "Constraint":
        text = (text or "").strip()
        if not text or text == "*":
            return cls(())
        comparators = []
        for part in text.split(","):
            part = part.strip()
            if not part:
                continue
            for op in _OPS:
                if part.startswith(op):
                    comparators.append(Comparator(op, Version.parse(part[len(op):])))
                    break
            else:
                raise ConstraintError(
                    f"invalid comparator {part!r}: expected one of {_OPS}"
                )
        return cls(tuple(comparators))

    def allows(self, version: Version) -> bool:
        return all(c.allows(version) for c in self.comparators)

    def __str__(self) -> str:
        return ",".join(str(c) for c in self.comparators) or "*"
