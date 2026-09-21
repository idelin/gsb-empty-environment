"""Conditions on dependency edges: target OS, architecture and build stage.

Every evaluation produces evidence (per-field checks) so the build report
can show exactly why an edge was taken or pruned.
"""
from __future__ import annotations

from dataclasses import dataclass

FIELDS = ("os", "arch", "stage")


@dataclass(frozen=True)
class Condition:
    os: tuple = ()
    arch: tuple = ()
    stage: tuple = ()

    @classmethod
    def from_json(cls, data) -> "Condition":
        data = data or {}
        unknown = set(data) - set(FIELDS)
        if unknown:
            raise ValueError(f"unknown condition fields: {sorted(unknown)}")

        def norm(value):
            if value is None:
                return ()
            if isinstance(value, str):
                return (value,)
            return tuple(value)

        return cls(norm(data.get("os")), norm(data.get("arch")), norm(data.get("stage")))

    def as_dict(self) -> dict:
        return {f: list(getattr(self, f)) for f in FIELDS if getattr(self, f)}


@dataclass(frozen=True)
class FieldCheck:
    field: str
    required: tuple
    actual: str
    passed: bool


@dataclass(frozen=True)
class Evaluation:
    condition: Condition
    checks: tuple  # of FieldCheck, one per constrained field

    @property
    def taken(self) -> bool:
        return all(check.passed for check in self.checks)


def evaluate(condition: Condition, context: dict) -> Evaluation:
    """Evaluate a condition against {os, arch, stage}; unconstrained fields pass."""
    checks = []
    for name in FIELDS:
        required = getattr(condition, name)
        if not required:
            continue
        actual = context[name]
        checks.append(FieldCheck(name, required, actual, actual in required))
    return Evaluation(condition, tuple(checks))
