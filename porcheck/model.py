"""Finite-state model definitions: states, actions, invariants.

A state is an immutable sorted tuple of (variable, value) pairs so it can be
hashed and stored in visited sets.  Actions declare their read/write variable
sets statically; the checker validates actual writes against the declaration.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Callable

State = tuple  # tuple[tuple[str, int], ...] sorted by variable name


def make_state(values: dict[str, int]) -> State:
    return tuple(sorted(values.items()))


def state_dict(state: State) -> dict[str, int]:
    return dict(state)


def format_state(state: State) -> str:
    return "{" + ", ".join(f"{k}={v}" for k, v in state) + "}"


@dataclass(frozen=True, eq=False)
class Action:
    """A guarded transition with a static read/write signature."""

    name: str
    reads: frozenset[str]
    writes: frozenset[str]
    guard: Callable[[dict[str, int]], bool]
    effect: Callable[[dict[str, int]], dict[str, int]]

    def enabled(self, state: State) -> bool:
        return bool(self.guard(state_dict(state)))

    def apply(self, state: State) -> State:
        before = state_dict(state)
        after = self.effect(dict(before))
        changed = {k for k in after if after[k] != before.get(k)}
        changed |= {k for k in before if k not in after}
        undeclared = changed - set(self.writes)
        if undeclared:
            raise ValueError(
                f"action {self.name!r} modified undeclared variables: "
                f"{sorted(undeclared)}"
            )
        return make_state(after)


@dataclass(frozen=True)
class Invariant:
    """A safety property over a declared set of variables."""

    name: str
    variables: frozenset[str]
    predicate: Callable[[dict[str, int]], bool]

    def holds(self, state: State) -> bool:
        return bool(self.predicate(state_dict(state)))


@dataclass
class Model:
    name: str
    initial: dict[str, int]
    actions: tuple[Action, ...]
    invariants: tuple[Invariant, ...] = ()
    deadlock_is_error: bool = True

    @property
    def initial_state(self) -> State:
        return make_state(self.initial)

    @property
    def property_variables(self) -> frozenset[str]:
        variables: set[str] = set()
        for invariant in self.invariants:
            variables |= set(invariant.variables)
        return frozenset(variables)
