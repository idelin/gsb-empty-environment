"""Finite-state model definition and deterministic replay engine.

A *scenario* (initial-config overrides, action trace, fault set) is replayed
against a fixed :class:`Model`.  Replay is deterministic: the same scenario
always yields the same :class:`ReplayResult`.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Callable, Mapping

VIOLATION = "violation"  # a property error fired during replay
CLEAN = "clean"          # replay completed without any property error
INVALID = "invalid"      # an action precondition failed (trace not executable)

Precondition = Callable[[Mapping[str, Any], frozenset], bool]
Effect = Callable[[dict, frozenset], None]
ErrorPredicate = Callable[[Mapping[str, Any], frozenset], "str | None"]


@dataclass(frozen=True)
class Action:
    """A named transition: enabled when ``pre`` holds, applies ``eff``."""

    name: str
    pre: Precondition
    eff: Effect


@dataclass
class Model:
    """A fixed finite-state model.

    ``defaults``   : full default initial configuration.
    ``actions``    : action name -> Action.
    ``error_of``   : maps a state (+ active faults) to an *error signature*
                     string, or None when no property is violated.  The
                     signature identifies the violation: two replays are the
                     same counterexample only when signatures are equal.
    """

    name: str
    defaults: dict
    actions: dict
    error_of: ErrorPredicate


@dataclass
class ReplayResult:
    status: str            # VIOLATION | CLEAN | INVALID
    error: "str | None"    # error signature when status == VIOLATION
    state: dict            # state at which replay stopped
    steps: int             # number of actions successfully executed

    @property
    def is_violation(self) -> bool:
        return self.status == VIOLATION


def replay(model: Model, config: Mapping[str, Any], actions, faults) -> ReplayResult:
    """Replay ``actions`` from ``config`` (overrides on model defaults).

    The property predicate is checked on the initial state and after every
    action.  If an action's precondition does not hold, the trace is not
    executable and the result is INVALID -- such a scenario can never be
    accepted as a counterexample.
    """
    state = dict(model.defaults)
    state.update(config)
    faults = frozenset(faults)
    error = model.error_of(state, faults)
    if error is not None:
        return ReplayResult(VIOLATION, error, state, 0)
    for steps, name in enumerate(actions, start=1):
        action = model.actions.get(name)
        if action is None or not action.pre(state, faults):
            return ReplayResult(INVALID, None, state, steps - 1)
        action.eff(state, faults)
        error = model.error_of(state, faults)
        if error is not None:
            return ReplayResult(VIOLATION, error, state, steps)
    return ReplayResult(CLEAN, None, state, len(actions))
