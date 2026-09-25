"""Stable, versioned entities for the mini truth-maintenance system.

An *environment* is the frozenset of external fact IDs on which a belief
rests.  A belief's *label* is the antichain of its minimal supporting
environments: every listed environment independently proves the belief,
and no listed environment contains another one.
"""
from __future__ import annotations

from dataclasses import dataclass, dataclass as _dc  # noqa: F401
from typing import Tuple

Environment = frozenset


def minimize_envs(envs) -> frozenset:
    """Return the inclusion-minimal members of a collection of environments."""
    items = list(frozenset(envs))
    return frozenset(e for e in items if not any(other < e for other in items))


def _nonempty(name: str, value: str) -> None:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{name} must be a non-empty string")


@dataclass(frozen=True, slots=True)
class Fact:
    """An externally asserted fact (stable ID, monotonically versioned)."""

    id: str
    atom: str
    active: bool = True
    version: int = 1

    def __post_init__(self) -> None:
        _nonempty("fact id", self.id)
        _nonempty("atom", self.atom)
        if self.version < 1:
            raise ValueError("fact version must start at 1")


@dataclass(frozen=True, slots=True)
class Rule:
    """A conjunctive Horn rule: all premises together imply the conclusion.

    An empty premise tuple is an axiom whose conclusion holds with the empty
    environment (no external fact needed).
    """

    id: str
    premises: Tuple[str, ...]
    conclusion: str
    version: int = 1

    def __post_init__(self) -> None:
        _nonempty("rule id", self.id)
        _nonempty("conclusion", self.conclusion)
        if any(not isinstance(p, str) or not p for p in self.premises):
            raise ValueError("rule premises must be non-empty strings")
        if self.version < 1:
            raise ValueError("rule version must start at 1")
        object.__setattr__(self, "premises", tuple(self.premises))


@dataclass(frozen=True, slots=True)
class Step:
    """A recorded derivation of a rule firing (one step per rule).

    The step keeps the rule's stable ID.  Its version starts at 0 while the
    rule cannot fire, becomes 1 on the first firing, and is incremented on
    every active/inactive transition afterwards.
    """

    rule_id: str
    conclusion: str
    premises: Tuple[str, ...]
    active: bool
    version: int
