"""Version constraints: ranges (>=, <=, >, <), pins (==) and exclusions (!=)."""

from __future__ import annotations

from dataclasses import dataclass

from .version import Version

_OPERATORS = (">=", "<=", "==", "!=", ">", "<")


@dataclass(frozen=True)
class Comparator:
    op: str
    version: Version

    def matches(self, version: Version) -> bool:
        if self.op == "==":
            return version == self.version
        if self.op == "!=":
            return version != self.version
        if self.op == ">=":
            return version >= self.version
        if self.op == "<=":
            return version <= self.version
        if self.op == ">":
            return version > self.version
        if self.op == "<":
            return version < self.version
        raise ValueError(f"unknown operator: {self.op!r}")

    def __str__(self) -> str:
        return f"{self.op}{self.version}"


@dataclass(frozen=True)
class Constraint:
    """A conjunction of comparators; empty means "any version"."""

    comparators: tuple[Comparator, ...] = ()

    @classmethod
    def parse(cls, text: str | None) -> "Constraint":
        text = (text or "").strip()
        if text in ("", "*"):
            return cls(())
        parts = [piece for chunk in text.split(",") for piece in chunk.split()]
        comparators = []
        for part in parts:
            for op in _OPERATORS:
                if part.startswith(op):
                    comparators.append(Comparator(op, Version.parse(part[len(op):])))
                    break
            else:
                comparators.append(Comparator("==", Version.parse(part)))
        return cls(tuple(comparators))

    def satisfied_by(self, version: Version) -> bool:
        return all(comp.matches(version) for comp in self.comparators)

    def allows_prerelease(self) -> bool:
        """A prerelease is only eligible when a comparator names one."""
        return any(comp.version.is_prerelease for comp in self.comparators)

    def __str__(self) -> str:
        if not self.comparators:
            return "any"
        return ", ".join(str(comp) for comp in self.comparators)
