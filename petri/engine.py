"""Exact reachability search over the finite state space of a bounded net.

Because every place has a finite capacity, the reachable state space is
finite and the search is exact -- bounded places are never "rounded up" to
an unbounded net, so no coverability-style over-approximation is produced.

If the caller truncates the search with ``max_states``, the result stays
PENDING and no verdict is derived from the partial exploration.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from typing import Callable, Iterable

from .model import PetriNet

COMPLETE = "COMPLETE"
PENDING = "PENDING"

EXACT_SEMANTICS = "exact-bounded-reachability"


class ReplayError(Exception):
    """A counterexample trace failed to replay on the net."""


@dataclass
class SearchResult:
    net: PetriNet
    status: str  # COMPLETE | PENDING
    states: set[tuple[int, ...]]  # state keys (canonical if reduced)
    representatives: dict[tuple[int, ...], tuple[int, ...]]  # key -> real marking
    parent: dict[tuple[int, ...], tuple[tuple[int, ...], str]]
    reduction: object | None = None
    violation_key: tuple[int, ...] | None = None
    semantics: str = EXACT_SEMANTICS

    @property
    def complete(self) -> bool:
        return self.status == COMPLETE

    def concrete(self, key: tuple[int, ...]) -> tuple[int, ...]:
        """The real (non-canonical) marking stored for a state key."""
        return self.representatives[key]

    def concrete_dict(self, key: tuple[int, ...]) -> dict[str, int]:
        """Real marking with real place IDs for a state key."""
        return self.net.marking_dict(self.representatives[key])

    def trace_to(self, key: tuple[int, ...]) -> list[str]:
        """Transition names leading from the initial marking to ``key``."""
        trace: list[str] = []
        while key in self.parent:
            key, tname = self.parent[key]
            trace.append(tname)
        trace.reverse()
        return trace

    def deadlock_keys(self) -> list[tuple[int, ...]]:
        """States whose representative has no enabled transition."""
        return [
            k
            for k in self.states
            if not self.net.enabled_transitions(self.representatives[k])
        ]


def reachability(
    net: PetriNet,
    max_states: int | None = None,
    reduction=None,
    violation: Callable[[tuple[int, ...]], bool] | None = None,
) -> SearchResult:
    """BFS over reachable markings.

    ``reduction`` (optional) provides a ``canonical(marking)`` function;
    states are then keyed by canonical form while a concrete, genuinely
    reachable representative marking is kept for every key.

    ``violation`` (optional) is evaluated on concrete markings; the search
    stops as soon as it returns True and records the offending state.
    """
    canon = reduction.canonical if reduction is not None else (lambda m: m)

    start_key = canon(net.initial)
    states = {start_key}
    representatives = {start_key: net.initial}
    parent: dict[tuple[int, ...], tuple[tuple[int, ...], str]] = {}
    queue: deque[tuple[int, ...]] = deque([start_key])
    status = COMPLETE
    violation_key = None

    if violation is not None and violation(net.initial):
        violation_key = start_key
        queue.clear()

    while queue:
        if max_states is not None and len(states) >= max_states:
            # Exploration truncated: the model is only partially searched,
            # so the outcome stays pending.
            status = PENDING
            break
        key = queue.popleft()
        rep = representatives[key]
        for t in net.transitions.values():
            if not net.is_enabled(t, rep):
                continue
            nxt = net.fire(t, rep)
            nkey = canon(nxt)
            if nkey in states:
                continue
            states.add(nkey)
            representatives[nkey] = nxt
            parent[nkey] = (key, t.name)
            if violation is not None and violation(nxt):
                violation_key = nkey
                queue.clear()
                break
            queue.append(nkey)

    return SearchResult(
        net=net,
        status=status,
        states=states,
        representatives=representatives,
        parent=parent,
        reduction=reduction,
        violation_key=violation_key,
    )


@dataclass
class Counterexample:
    """A replayable witness: a trace plus the reached real marking."""

    trace: list[str]
    marking: dict[str, int]  # real place IDs

    def replay(self, net: PetriNet) -> dict[str, int]:
        return replay_dict(net, self.trace)


@dataclass
class InvariantResult:
    status: str  # COMPLETE | PENDING
    holds: bool | None  # None while the search is pending
    counterexample: Counterexample | None
    explored: int
    description: str = ""


def check_invariant(
    net: PetriNet,
    predicate: Callable[[dict[str, int]], bool],
    max_states: int | None = None,
    reduction=None,
    description: str = "invariant",
) -> InvariantResult:
    """Check ``predicate`` on every reachable marking.

    The predicate receives markings as dicts keyed by real place IDs. With a
    reduction, it is evaluated on concrete representative markings, so any
    reported counterexample is a real reachable state; for *completeness*
    under reduction the predicate should be symmetric.
    """
    result = reachability(
        net,
        max_states=max_states,
        reduction=reduction,
        violation=lambda m: not predicate(net.marking_dict(m)),
    )
    if result.violation_key is not None:
        cex = Counterexample(
            trace=result.trace_to(result.violation_key),
            marking=result.concrete_dict(result.violation_key),
        )
        return InvariantResult(COMPLETE, False, cex, len(result.states), description)
    if result.status == PENDING:
        return InvariantResult(PENDING, None, None, len(result.states), description)
    return InvariantResult(COMPLETE, True, None, len(result.states), description)


def replay(net: PetriNet, trace: Iterable[str]) -> tuple[int, ...]:
    """Replay a transition trace from the initial marking.

    Raises ReplayError if any step is disabled -- a genuine counterexample
    must always replay cleanly.
    """
    m = net.initial
    for step, name in enumerate(trace):
        if name not in net.transitions:
            raise ReplayError(f"step {step}: unknown transition {name!r}")
        t = net.transitions[name]
        if not net.is_enabled(t, m):
            raise ReplayError(
                f"step {step}: transition {name!r} not enabled at "
                f"{net.marking_dict(m)}"
            )
        m = net.fire(t, m)
    return m


def replay_dict(net: PetriNet, trace: Iterable[str]) -> dict[str, int]:
    return net.marking_dict(replay(net, trace))


@dataclass
class CrossCheckReport:
    status: str  # COMPLETE | PENDING
    ok: bool | None  # None while pending
    plain_states: int
    reduced_states: int
    details: str = ""


def cross_check(net: PetriNet, max_states: int | None = None) -> CrossCheckReport:
    """Independent cross-check of reduced search against plain search.

    Runs an unreduced search as ground truth and verifies that (1) every
    concrete representative of the reduced search is genuinely reachable,
    and (2) every reachable marking's canonical form is covered by the
    reduced search. Intended for small nets.
    """
    from .symmetry import SymmetryReduction

    reduction = SymmetryReduction(net)
    plain = reachability(net, max_states=max_states)
    reduced = reachability(net, max_states=max_states, reduction=reduction)
    if plain.status == PENDING or reduced.status == PENDING:
        return CrossCheckReport(
            PENDING, None, len(plain.states), len(reduced.states),
            "search truncated; cross-check inconclusive",
        )
    reps_ok = all(rep in plain.states for rep in reduced.representatives.values())
    cover_ok = all(reduction.canonical(s) in reduced.states for s in plain.states)
    details = []
    if not reps_ok:
        details.append("reduced search produced an unreachable representative")
    if not cover_ok:
        details.append("reduced search missed a reachable canonical class")
    return CrossCheckReport(
        COMPLETE,
        reps_ok and cover_ok,
        len(plain.states),
        len(reduced.states),
        "; ".join(details) or "reduced and plain searches agree",
    )
