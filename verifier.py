"""Bounded explicit-state model checker with a three-valued verdict.

The verification target is exactly the given finite-state model: BFS over
reachable states, for every subset of the declared fault universe.

* ``violated``  : a state whose error predicate holds was reached; the
  returned counterexample scenario is replayable by construction.
* ``safe``      : every fault subset was explored exhaustively within the
  depth/state budgets and no violation exists.
* ``undecided`` : the state or depth budget cut the search short.  An
  unfinished search is never reported as safe.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from itertools import combinations

from model import Model
from shrinker import Scenario

SAFE = "safe"
VIOLATED = "violated"
UNDECIDED = "undecided"


@dataclass
class VerificationResult:
    status: str                     # SAFE | VIOLATED | UNDECIDED
    error: "str | None"             # error signature when VIOLATED
    counterexample: "Scenario | None"
    states_explored: int
    truncated: bool                 # True when a budget cut the search short

    def summary(self) -> str:
        lines = [f"verdict         : {self.status}",
                 f"states explored : {self.states_explored}"]
        if self.status == VIOLATED:
            lines.append(f"error signature : {self.error}")
            lines.append("counterexample (replayable):")
            lines.append(self.counterexample.describe())
        elif self.status == UNDECIDED:
            lines.append("search incomplete: state/depth budget exhausted; "
                         "no conclusion about safety is drawn")
        return "\n".join(lines)


def _state_key(state: dict) -> tuple:
    return tuple(sorted(state.items()))


def _fault_subsets(universe):
    universe = sorted(universe)
    for size in range(len(universe) + 1):
        for combo in combinations(universe, size):
            yield frozenset(combo)


def verify(model: Model, *, config=None, fault_universe=(),
           max_states: int = 10000, max_depth: int = 64) -> VerificationResult:
    base = dict(model.defaults)
    base.update(config or {})
    overrides = dict(config or {})
    explored = 0

    for faults in _fault_subsets(fault_universe):
        error = model.error_of(base, faults)
        if error is not None:
            return VerificationResult(
                VIOLATED, error, Scenario(overrides, [], faults), explored, False)

        start_key = _state_key(base)
        visited = {start_key}
        parents = {start_key: None}
        queue = deque([(start_key, base, 0)])
        truncated = False

        while queue:
            key, state, depth = queue.popleft()
            if depth >= max_depth:
                # Unexpanded frontier: if anything is still enabled here the
                # search is incomplete.
                if any(a.pre(state, faults) for a in model.actions.values()):
                    truncated = True
                continue
            if explored >= max_states:
                truncated = True
                break
            explored += 1
            for name, action in model.actions.items():
                if not action.pre(state, faults):
                    continue
                nxt = dict(state)
                action.eff(nxt, faults)
                nkey = _state_key(nxt)
                if nkey in visited:
                    continue
                error = model.error_of(nxt, faults)
                visited.add(nkey)
                parents[nkey] = (key, name)
                if error is not None:
                    actions = []
                    cursor = nkey
                    while parents[cursor] is not None:
                        parent_key, action_name = parents[cursor]
                        actions.append(action_name)
                        cursor = parent_key
                    actions.reverse()
                    return VerificationResult(
                        VIOLATED, error,
                        Scenario(overrides, actions, faults),
                        explored, False)
                queue.append((nkey, nxt, depth + 1))

        if truncated:
            return VerificationResult(UNDECIDED, None, None, explored, True)

    return VerificationResult(SAFE, None, None, explored, False)
