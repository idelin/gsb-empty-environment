"""Finite-state model: fixed process roles, shared variables, transitions.

A :class:`State` is an immutable pair ``(shared, locals)``:

* ``shared`` is a tuple of shared-variable values; positions listed in
  ``pid_vars`` hold process IDs (with :data:`NO_PID` meaning "no process").
* ``locals[i]`` is the local state of process ``i``.

Each :class:`Transition` is enabled per process. A transition may restrict
the roles that are allowed to execute it; this is how special roles (for
example a single leader) are declared rather than silently assumed.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, FrozenSet, Optional, Tuple

NO_PID = -1


@dataclass(frozen=True)
class State:
    """Immutable system state."""

    shared: tuple
    locals: tuple


@dataclass(frozen=True)
class Transition:
    """A process-indexed transition schema.

    ``roles`` is the set of roles that may execute it, or ``None`` if any
    role may. ``guard(pid, state)`` and ``effect(pid, state)`` are plain
    functions returning a bool / a new :class:`State`.
    """

    name: str
    guard: Callable[[int, State], bool]
    effect: Callable[[int, State], State]
    roles: Optional[FrozenSet[str]] = None


class Model:
    """A closed, finite-state model to be verified."""

    def __init__(
        self,
        roles: Tuple[str, ...],
        initial: State,
        transitions: Tuple[Transition, ...],
        pid_vars: Tuple[int, ...] = (),
    ) -> None:
        self.roles = tuple(roles)
        self.initial = initial
        self.transitions = tuple(transitions)
        self.pid_vars = frozenset(pid_vars)
        if len(initial.locals) != len(self.roles):
            raise ValueError("number of local states must match number of roles")

    @property
    def num_processes(self) -> int:
        return len(self.roles)

    def enabled(self, pid: int, transition: Transition, state: State) -> bool:
        role_ok = transition.roles is None or self.roles[pid] in transition.roles
        return role_ok and bool(transition.guard(pid, state))

    def successors(self, state: State):
        """Return ``[(action, next_state), ...]`` with ``action=(pid, name)``."""
        result = []
        for pid in range(self.num_processes):
            for transition in self.transitions:
                if self.enabled(pid, transition, state):
                    result.append(
                        ((pid, transition.name), transition.effect(pid, state))
                    )
        return result
