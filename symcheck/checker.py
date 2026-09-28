"""Explicit-state verification with optional symmetry reduction.

Verdicts are three-valued:

* :data:`SAFE`      - the reachable state space was fully explored and the
                       invariant held everywhere;
* :data:`VIOLATED`  - a violating state was reached; the counterexample is a
                       concrete, replayable trace plus an ID mapping per state;
* :data:`PENDING`   - the exploration budget ran out before the search was
                       complete, so no safety claim is made.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import Callable, List, Optional, Tuple

from .model import Model, State
from .symmetry import canonicalize

SAFE = "safe"
VIOLATED = "violated"
PENDING = "pending"


@dataclass(frozen=True)
class Counterexample:
    """A concrete counterexample trace.

    ``states[i]`` and ``actions[i]`` give the original-ID states and the
    action between them; ``id_maps[i]`` is the permutation mapping each
    original process ID to its canonical representative, so the mapping is
    preserved and either representation can be restored.
    """

    states: List[State]
    actions: List[Tuple[int, str]]
    id_maps: List[Tuple[int, ...]]


@dataclass(frozen=True)
class Verdict:
    status: str
    explored: int
    counterexample: Optional[Counterexample] = None

    @property
    def decided(self) -> bool:
        return self.status != PENDING


def _build_trace(bad_canonical, parent, representatives, id_maps):
    canonical_chain = []
    node = bad_canonical
    while node is not None:
        canonical_chain.append(node)
        entry = parent[node]
        node = None if entry is None else entry[0]
    canonical_chain.reverse()

    states = [representatives[c] for c in canonical_chain]
    maps = [id_maps[c] for c in canonical_chain]
    actions = []
    for canonical in canonical_chain[1:]:
        actions.append(parent[canonical][1])
    return Counterexample(states=states, actions=actions, id_maps=maps)


def verify(
    model: Model,
    invariant: Callable[[State], bool],
    use_symmetry: bool = True,
    max_states: Optional[int] = None,
) -> Verdict:
    """Breadth-first invariant check over a single finite :class:`Model`.

    With ``use_symmetry=True`` each reached state is folded onto its
    canonical representative. Only :data:`VIOLATED` / :data:`SAFE` are
    reported on complete evidence; an unfinished search is :data:`PENDING`.
    """
    identity = tuple(range(model.num_processes))

    def to_canonical(state: State):
        if use_symmetry:
            return canonicalize(state, model.roles, model.pid_vars)
        return state, identity

    initial_canonical, initial_perm = to_canonical(model.initial)
    seen = {initial_canonical}
    representatives = {initial_canonical: model.initial}
    id_maps = {initial_canonical: initial_perm}
    parent = {initial_canonical: None}
    queue = deque([initial_canonical])

    while queue:
        canonical = queue.popleft()
        concrete = representatives[canonical]
        if not invariant(concrete):
            return Verdict(
                VIOLATED,
                len(seen),
                _build_trace(canonical, parent, representatives, id_maps),
            )
        for action, next_state in model.successors(concrete):
            next_canonical, next_perm = to_canonical(next_state)
            if next_canonical in seen:
                continue
            if max_states is not None and len(seen) >= max_states:
                return Verdict(PENDING, len(seen))
            seen.add(next_canonical)
            representatives[next_canonical] = next_state
            id_maps[next_canonical] = next_perm
            parent[next_canonical] = (canonical, action)
            queue.append(next_canonical)

    return Verdict(SAFE, len(seen))


def replay(model: Model, actions: List[Tuple[int, str]]) -> List[State]:
    """Replay a concrete action list from the initial state.

    Raises ``ValueError`` if an action is not enabled, otherwise returns
    the list of visited states (including the initial state).
    """
    states = [model.initial]
    current = model.initial
    for pid, name in actions:
        matching = [t for t in model.transitions if t.name == name]
        transition = next(
            (t for t in matching if model.enabled(pid, t, current)), None
        )
        if transition is None:
            raise ValueError(f"action {name!r} of process {pid} not enabled")
        current = transition.effect(pid, current)
        states.append(current)
    return states
