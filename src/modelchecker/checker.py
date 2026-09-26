"""Explicit-state safety model checker.

The checker performs breadth-first exploration of the reachable state set.
It emits either:

* a *reachability-closure certificate* (every reachable state, proven safe,
  with every successor listed), or
* a *counterexample certificate* (a replayable initial-to-bad path), or
* an *unknown* result when the expansion budget is exhausted first.

A budget-limited partial exploration is never packaged as a safe result.
"""

from __future__ import annotations

import json
from collections import deque
from dataclasses import dataclass, field
from enum import Enum
from typing import Optional

from .model import FiniteModel

CERTIFICATE_VERSION = 1


class CheckOutcome(str, Enum):
    SAFE = "safe"
    UNSAFE = "unsafe"
    UNKNOWN = "unknown"


@dataclass
class CheckResult:
    outcome: CheckOutcome
    model: FiniteModel
    explored: frozenset[object]
    budget: Optional[int]
    expansions: int
    certificate: Optional[dict] = None
    counterexample: tuple[object, ...] = ()
    reason: str = ""

    @property
    def is_decided(self) -> bool:
        return self.outcome is not CheckOutcome.UNKNOWN


def reachable_sets(
    model: FiniteModel, budget: Optional[int] = None
) -> tuple[set[object], set[object], Optional[object], int]:
    """BFS exploration.

    Returns ``(explored, frontier, first_bad, expansions)``.  ``frontier``
    holds explored states whose successors have not all been expanded; when
    it is empty and no bad state was found the exploration is complete.
    ``budget`` bounds the number of successor expansions.
    """
    if budget is not None and budget < 0:
        raise ValueError("budget must be non-negative or None")
    explored: set[object] = set()
    queue: deque[object] = deque()
    for state in model.initial:
        if state not in explored:
            explored.add(state)
            queue.append(state)
    expansions = 0
    first_bad: Optional[object] = None
    while queue:
        if first_bad is not None:
            break
        if budget is not None and expansions >= budget:
            break
        state = queue.popleft()
        expansions += 1
        if model.is_bad(state) and first_bad is None:
            first_bad = state
            break
        for nxt in model.successors(state):
            if nxt not in explored:
                explored.add(nxt)
                queue.append(nxt)
    return explored, set(queue), first_bad, expansions


def _shortest_path(model: FiniteModel, target: object) -> tuple[object, ...]:
    parent: dict[object, Optional[object]] = {s: None for s in model.initial}
    if target in parent:
        return (target,)
    queue: deque[object] = deque(model.initial)
    while queue:
        state = queue.popleft()
        for nxt in model.successors(state):
            if nxt not in parent:
                parent[nxt] = state
                if nxt == target:
                    path: list[object] = [nxt]
                    node = state
                    while node is not None:
                        path.append(node)
                        node = parent[node]
                    return tuple(reversed(path))
                queue.append(nxt)
    raise RuntimeError(f"target {target!r} is not reachable")  # pragma: no cover


def _safe_certificate(
    model: FiniteModel, explored: frozenset[object], expansions: int
) -> dict:
    ordered = sorted(explored, key=lambda s: json.dumps(s, sort_keys=True))
    return {
        "certificate_version": CERTIFICATE_VERSION,
        "kind": "reachability_closure",
        "model": model.to_certificate_model(),
        "generator_stats": {
            "reachable_count": len(ordered),
            "expansions": expansions,
        },
        "initial": sorted(
            model.initial, key=lambda s: json.dumps(s, sort_keys=True)
        ),
        "reachable_states": ordered,
        "successors": {
            json.dumps(state, sort_keys=True): list(model.successors(state))
            for state in ordered
        },
    }


def _counterexample_certificate(
    model: FiniteModel, path: tuple[object, ...], expansions: int
) -> dict:
    transitions = []
    for index, (source, target) in enumerate(zip(path, path[1:])):
        transitions.append(
            {
                "index": index,
                "source": source,
                "target": target,
                "action": f"step_{index}",
            }
        )
    return {
        "certificate_version": CERTIFICATE_VERSION,
        "kind": "counterexample",
        "model": model.to_certificate_model(),
        "generator_stats": {"path_length": len(path), "expansions": expansions},
        "path": list(path),
        "transitions": transitions,
        "bad_state": path[-1],
    }


def check_safety(model: FiniteModel, budget: Optional[int] = None) -> CheckResult:
    """Check the invariant over reachable states, with an optional BFS budget."""
    explored, frontier, first_bad, expansions = reachable_sets(model, budget)
    if first_bad is not None:
        path = _shortest_path(model, first_bad)
        cert = _counterexample_certificate(model, path, expansions)
        return CheckResult(
            outcome=CheckOutcome.UNSAFE,
            model=model,
            explored=explored,
            budget=budget,
            expansions=expansions,
            certificate=cert,
            counterexample=path,
        )
    if frontier:
        return CheckResult(
            outcome=CheckOutcome.UNKNOWN,
            model=model,
            explored=frozenset(explored),
            budget=budget,
            expansions=expansions,
            reason="budget exhausted before the reachable set was closed",
        )
    cert = _safe_certificate(model, frozenset(explored), expansions)
    return CheckResult(
        outcome=CheckOutcome.SAFE,
        model=model,
        explored=frozenset(explored),
        budget=budget,
        expansions=expansions,
        certificate=cert,
    )


def explore_reachable(
    model: FiniteModel, budget: Optional[int] = None
) -> CheckResult:
    """Alias kept for readability in callers and tests."""
    return check_safety(model, budget)
