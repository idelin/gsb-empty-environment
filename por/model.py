"""Finite-state model definitions used by the POR verifier.

A state is an immutable, hashable tuple of ``(variable, value)`` pairs sorted
by variable name.  Actions carry explicit read/write sets; the partial order
reduction derives independence purely from these sets.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, Optional

State = tuple  # tuple[tuple[str, object], ...], sorted by variable name


def make_state(mapping) -> State:
    return tuple(sorted(mapping.items()))


def state_dict(state: State) -> dict:
    return dict(state)


def format_state(state: State) -> str:
    return "{" + ", ".join(f"{k}={v}" for k, v in state) + "}"


@dataclass(frozen=True)
class Action:
    """A guarded transition with a declared read/write footprint."""

    name: str
    guard: Callable[[State], bool]
    effect: Callable[[State], State]
    reads: frozenset
    writes: frozenset


@dataclass
class Model:
    """A finite-state transition system plus an optional safety property.

    ``property_fn`` is a state predicate (the safety invariant).  The search
    reports a violation when it reaches a state where the predicate is false.
    ``property_vars`` must list every variable the predicate inspects; it is
    used for the visibility condition of the reduction.
    """

    name: str
    variables: tuple
    initial: State
    actions: tuple
    property_fn: Optional[Callable[[State], bool]] = None
    property_vars: frozenset = frozenset()
    property_name: str = ""
    check_deadlock: bool = True

    def __post_init__(self):
        self.by_name = {a.name: a for a in self.actions}
        if len(self.by_name) != len(self.actions):
            raise ValueError("duplicate action names")
