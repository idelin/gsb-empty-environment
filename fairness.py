"""Fair-cycle (lasso) witness checker for weak/strong fairness constraints.

Given a finite-state transition system and a set of fairness constraints on
actions, this module searches for infinite-loop witnesses (lassos: a finite
stem plus a cycle that repeats forever) that violate the constraints.

Fairness semantics on the infinite execution ``stem + cycle * inf``:

- Weak fairness of action ``a`` is violated iff ``a`` is *continuously*
  enabled (enabled at every state of the cycle) but never taken in the cycle.
- Strong fairness of ``a`` is violated iff ``a`` is *intermittently* enabled
  (enabled at at least one state of the cycle, hence infinitely often when
  the cycle repeats) but never taken in the cycle.

Continuous enablement must not be confused with intermittent enablement:
only the former violates weak fairness; the latter concerns strong fairness
only.  Every witness records the exact enablement pattern of its action
along the cycle so the two cases stay distinct.

Verdicts:

- ``VIOLATED``: a replayable counterexample lasso was found.
- ``HOLDS``:    the reachable state space was exhausted and no lasso exists.
- ``PENDING``:  the search was cut short (state limit); no conclusive answer.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from enum import Enum
from typing import Hashable, Iterable, Optional

State = Hashable
Action = Hashable
Transition = tuple  # (src, action, dst)


class FairnessKind(Enum):
    WEAK = "weak"
    STRONG = "strong"


class Verdict(Enum):
    HOLDS = "holds"
    VIOLATED = "violated"
    PENDING = "pending"


class TransitionSystem:
    """A finite-state transition system with action-labeled transitions."""

    def __init__(
        self,
        initials: Iterable[State],
        transitions: Iterable[Transition],
    ) -> None:
        self.initials = list(initials)
        self._succ: dict[State, list[tuple[Action, State]]] = {}
        for src, act, dst in transitions:
            self._succ.setdefault(src, []).append((act, dst))
            self._succ.setdefault(dst, [])
        for init in self.initials:
            self._succ.setdefault(init, [])

    def states(self) -> Iterable[State]:
        return self._succ.keys()

    def successors(self, state: State) -> list[tuple[Action, State]]:
        return self._succ.get(state, [])

    def enabled(self, state: State) -> set[Action]:
        """Actions with at least one outgoing transition at ``state``."""
        return {act for act, _ in self._succ.get(state, [])}


@dataclass
class CycleStats:
    """Enablement pattern of one action along a witness cycle.

    ``continuously_enabled`` and ``intermittently_enabled`` are mutually
    exclusive by construction; they are never conflated.
    """

    action: Action
    cycle_states: list[State]
    enabled_at: list[State]
    taken_in_cycle: bool

    @property
    def continuously_enabled(self) -> bool:
        return bool(self.cycle_states) and len(self.enabled_at) == len(self.cycle_states)

    @property
    def intermittently_enabled(self) -> bool:
        return 0 < len(self.enabled_at) < len(self.cycle_states)

    def describe(self) -> str:
        total = len(self.cycle_states)
        hits = len(self.enabled_at)
        if self.continuously_enabled:
            pattern = "continuously enabled"
        elif self.intermittently_enabled:
            pattern = "intermittently enabled"
        else:
            pattern = "never enabled"
        taken = "taken" if self.taken_in_cycle else "never taken"
        return f"{pattern} ({hits}/{total} cycle states), {taken} in cycle"


@dataclass
class Witness:
    """A replayable lasso: finite stem plus a repeatable cycle."""

    action: Action
    kind: FairnessKind
    stem: list[Transition]
    cycle: list[Transition]
    stats: CycleStats


@dataclass
class ReplayResult:
    ok: bool
    error: Optional[str] = None
    visited: list[State] = field(default_factory=list)


@dataclass
class FairnessReport:
    action: Action
    kind: FairnessKind
    verdict: Verdict
    witness: Optional[Witness]
    states_explored: int
    search_exhausted: bool


def reachable(ts: TransitionSystem, max_states: int) -> tuple[set[State], bool]:
    """BFS from the initial states.

    Returns the explored states and whether the search was exhaustive.
    A ``False`` flag means the state budget was hit and results must stay
    pending unless a conclusive counterexample was found.
    """
    seen: set[State] = set()
    queue: deque[State] = deque()
    for init in ts.initials:
        if init not in seen:
            if len(seen) >= max_states:
                return seen, False
            seen.add(init)
            queue.append(init)
    while queue:
        src = queue.popleft()
        for _, dst in ts.successors(src):
            if dst not in seen:
                if len(seen) >= max_states:
                    return seen, False
                seen.add(dst)
                queue.append(dst)
    return seen, True


def _sccs(nodes: set[State], edges: dict[State, list[State]]) -> list[list[State]]:
    """Iterative Tarjan SCC decomposition."""
    index_of: dict[State, int] = {}
    lowlink: dict[State, int] = {}
    on_stack: set[State] = set()
    stack: list[State] = []
    components: list[list[State]] = []
    counter = 0
    for root in nodes:
        if root in index_of:
            continue
        index_of[root] = lowlink[root] = counter
        counter += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(edges.get(root, [])))]
        while work:
            node, it = work[-1]
            descended = False
            for nxt in it:
                if nxt not in nodes:
                    continue
                if nxt not in index_of:
                    index_of[nxt] = lowlink[nxt] = counter
                    counter += 1
                    stack.append(nxt)
                    on_stack.add(nxt)
                    work.append((nxt, iter(edges.get(nxt, []))))
                    descended = True
                    break
                if nxt in on_stack:
                    lowlink[node] = min(lowlink[node], index_of[nxt])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
            if lowlink[node] == index_of[node]:
                comp = []
                while True:
                    member = stack.pop()
                    on_stack.discard(member)
                    comp.append(member)
                    if member == node:
                        break
                components.append(comp)
    return components


def _closed_walk(start: State, adj: dict[State, list[tuple[Action, State]]]) -> list[Transition]:
    """A closed walk from ``start`` back to ``start`` inside one SCC.

    Repeating the walk forever yields a valid infinite execution, so it
    serves directly as the repeatable loop of a lasso witness.
    """
    for act, dst in adj.get(start, []):
        if dst == start:
            return [(start, act, start)]
    rev: dict[State, list[tuple[State, Action]]] = {}
    for src, edges in adj.items():
        for act, dst in edges:
            rev.setdefault(dst, []).append((src, act))
    prev: dict[State, Optional[tuple[State, Action]]] = {start: None}
    queue: deque[State] = deque([start])
    while queue:
        node = queue.popleft()
        for src, act in rev.get(node, []):
            if src not in prev:
                prev[src] = (node, act)
                queue.append(src)
    act0, nxt = adj[start][0]
    back: list[Transition] = []
    node = nxt
    while node != start:
        closer, act = prev[node]
        back.append((node, act, closer))
        node = closer
    return [(start, act0, nxt)] + back


def _stem_to(ts: TransitionSystem, reach: set[State], target: State) -> list[Transition]:
    """Shortest path of transitions from some initial state to ``target``."""
    prev: dict[State, Optional[tuple[State, Action]]] = {}
    queue: deque[State] = deque()
    for init in ts.initials:
        if init in reach and init not in prev:
            prev[init] = None
            queue.append(init)
    while queue:
        src = queue.popleft()
        if src == target:
            break
        for act, dst in ts.successors(src):
            if dst in reach and dst not in prev:
                prev[dst] = (src, act)
                queue.append(dst)
    path: list[Transition] = []
    node = target
    while prev[node] is not None:
        src, act = prev[node]
        path.append((src, act, node))
        node = src
    path.reverse()
    return path


def _stats(ts: TransitionSystem, action: Action, cycle: list[Transition]) -> CycleStats:
    cycle_states = [src for src, _, _ in cycle]
    enabled_at = [s for s in cycle_states if action in ts.enabled(s)]
    taken = any(act == action for _, act, _ in cycle)
    return CycleStats(action=action, cycle_states=cycle_states,
                      enabled_at=enabled_at, taken_in_cycle=taken)


def find_witness(
    ts: TransitionSystem,
    reach: set[State],
    action: Action,
    kind: FairnessKind,
) -> Optional[Witness]:
    """Search the explored states for a lasso violating the constraint.

    Weak fairness: the cycle stays inside states where ``action`` is enabled
    and never takes it (continuous enablement).
    Strong fairness: the cycle never takes ``action`` and passes at least one
    state where it is enabled (intermittent enablement, recurring forever).
    """
    if kind is FairnessKind.WEAK:
        nodes = {s for s in reach if action in ts.enabled(s)}
    else:
        nodes = set(reach)
    adj: dict[State, list[tuple[Action, State]]] = {
        s: [(act, dst) for act, dst in ts.successors(s)
            if act != action and dst in nodes]
        for s in nodes
    }
    edge_nodes = {s: [dst for _, dst in edges] for s, edges in adj.items()}
    for comp in _sccs(nodes, edge_nodes):
        has_loop = len(comp) > 1 or any(
            dst == comp[0] for _, dst in adj.get(comp[0], [])
        )
        if not has_loop:
            continue
        if kind is FairnessKind.STRONG:
            enabled_here = [s for s in comp if action in ts.enabled(s)]
            if not enabled_here:
                continue
            start = enabled_here[0]
        else:
            start = comp[0]
        cycle = _closed_walk(start, adj)
        stem = _stem_to(ts, reach, start)
        return Witness(action=action, kind=kind, stem=stem, cycle=cycle,
                       stats=_stats(ts, action, cycle))
    return None


def check_fairness(
    ts: TransitionSystem,
    constraints: Iterable[tuple[Action, FairnessKind]],
    max_states: int = 100_000,
) -> list[FairnessReport]:
    """Check each ``(action, kind)`` constraint on the finite-state model.

    A found lasso is conclusive (``VIOLATED``).  ``HOLDS`` is only reported
    after the reachable state space was exhausted; otherwise the constraint
    stays ``PENDING``.
    """
    reach, exhausted = reachable(ts, max_states)
    reports = []
    for action, kind in constraints:
        witness = find_witness(ts, reach, action, kind)
        if witness is not None:
            verdict = Verdict.VIOLATED
        elif not exhausted:
            verdict = Verdict.PENDING
        else:
            verdict = Verdict.HOLDS
        reports.append(FairnessReport(
            action=action, kind=kind, verdict=verdict, witness=witness,
            states_explored=len(reach), search_exhausted=exhausted,
        ))
    return reports


def replay(ts: TransitionSystem, witness: Witness) -> ReplayResult:
    """Re-execute a witness lasso and re-verify the fairness violation.

    Every step is checked against the model, the cycle must close, the
    constrained action must never be taken in the cycle, and the recorded
    enablement pattern (continuous vs. intermittent) must match the model
    exactly -- so a weak-fairness claim cannot be backed by merely
    intermittent enablement, or vice versa.
    """
    if not witness.cycle:
        return ReplayResult(False, "witness has no cycle")
    start = witness.stem[0][0] if witness.stem else witness.cycle[0][0]
    if start not in ts.initials:
        return ReplayResult(False, f"start state {start!r} is not initial")
    state = start
    visited = [state]
    for src, act, dst in list(witness.stem) + list(witness.cycle):
        if src != state:
            return ReplayResult(
                False, f"broken chain: expected to be at {state!r}, "
                f"transition starts at {src!r}")
        if (act, dst) not in ts.successors(src):
            return ReplayResult(
                False, f"invalid transition {src!r} --{act!r}--> {dst!r}")
        state = dst
        visited.append(state)
    if state != witness.cycle[0][0]:
        return ReplayResult(False, "cycle does not return to its start")
    action = witness.action
    for _, act, _ in witness.cycle:
        if act == action:
            return ReplayResult(
                False, f"action {action!r} is taken inside the cycle")
    recomputed = _stats(ts, action, witness.cycle)
    if witness.kind is FairnessKind.WEAK and not recomputed.continuously_enabled:
        return ReplayResult(
            False, f"weak fairness needs continuous enablement, but "
            f"'{action}' is only enabled at "
            f"{len(recomputed.enabled_at)}/{len(recomputed.cycle_states)} "
            f"cycle states")
    if witness.kind is FairnessKind.STRONG and not recomputed.enabled_at:
        return ReplayResult(
            False, f"strong fairness needs enablement somewhere on the "
            f"cycle, but '{action}' is never enabled there")
    recorded = witness.stats
    if (recorded.continuously_enabled != recomputed.continuously_enabled
            or recorded.intermittently_enabled != recomputed.intermittently_enabled):
        return ReplayResult(
            False, "recorded enablement pattern does not match the model")
    return ReplayResult(True, visited=visited)
