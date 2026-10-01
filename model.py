"""Finite-state transition system with bounded may/must completion checks.

Terminology
-----------
- terminal state: a declared end state (e.g. RESPONDED or CANCELLED). A
  request that reaches any terminal state is "complete"; *which* terminal
  state does not matter for completion.
- blocked state: a reachable non-terminal state with no outgoing transitions.
- may-complete: some path from the start state reaches a terminal state.
- must-complete (fair): every *fair* path from the start state reaches a
  terminal state. Fairness is weak fairness on actions: an infinite path on
  which a fair action is enabled forever but never taken is excluded.
- pending: the search bound was exhausted before a verdict could be given;
  no claim is made either way.

A transition system is a finite labelled graph. The checker explores it with
a bounded breadth-first search. If the bound is hit before the reachable
space is fully explored, the result is Status.PENDING.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from enum import Enum


class Status(Enum):
    """Verdict of a completion check."""

    GUARANTEED = "guaranteed"        # every fair path reaches a terminal state
    POSSIBLE_ONLY = "possible_only"  # some path completes, some fair path does not
    BLOCKED = "blocked"              # reachable dead end, or terminal unreachable
    PENDING = "pending"              # search bound exhausted, verdict unknown


@dataclass(frozen=True)
class Step:
    """One replayable transition: take `action`, arrive at `target`."""

    action: str
    target: str


@dataclass
class CheckResult:
    """Outcome of checking one start state against a transition system."""

    status: Status
    may_complete: bool | None        # None while PENDING
    must_complete_fair: bool | None  # None while PENDING
    blocked_states: frozenset[str]
    counterexample: tuple[Step, ...] | None  # replayable witness, if any
    cycle: bool                      # counterexample ends in a repeatable cycle
    explored_states: int
    note: str = ""


@dataclass(frozen=True)
class _Transition:
    action: str
    target: str
    fair: bool


class TransitionSystem:
    """A finite labelled transition system with fair actions."""

    def __init__(self, initial: str, terminals=()):
        self.initial = initial
        self.terminals = frozenset(terminals)
        self._transitions: dict[str, list[_Transition]] = {}

    def add(self, source: str, action: str, target: str, fair: bool = False) -> None:
        self._transitions.setdefault(source, []).append(_Transition(action, target, fair))

    def transitions(self, state: str) -> list[_Transition]:
        return list(self._transitions.get(state, ()))

    def actions(self, state: str) -> list[str]:
        return [t.action for t in self.transitions(state)]


def replay(system: TransitionSystem, start: str, trace) -> str:
    """Replay a trace of Steps from `start`; return the final state.

    Raises ValueError if any step is not enabled, so a counterexample that
    replays cleanly is a genuine witness of the model's behaviour.
    """
    state = start
    for i, step in enumerate(trace):
        enabled = system.transitions(state)
        if not any(t.action == step.action and t.target == step.target for t in enabled):
            raise ValueError(
                f"step {i}: action {step.action!r} -> {step.target!r} "
                f"not enabled in state {state!r}"
            )
        state = step.target
    return state


def check(system: TransitionSystem, start: str | None = None, max_states: int = 10_000) -> CheckResult:
    """Check whether `start` may/must (fairly) reach a terminal state.

    The search is bounded by `max_states`; if the reachable space is not
    fully explored within the bound the verdict is Status.PENDING.
    """
    start = system.initial if start is None else start

    # --- bounded BFS over the reachable space -------------------------------
    seen = {start}
    frontier = deque([start])
    edges: dict[str, list[_Transition]] = {}
    while frontier:
        if len(seen) > max_states:
            return CheckResult(
                status=Status.PENDING,
                may_complete=None,
                must_complete_fair=None,
                blocked_states=frozenset(),
                counterexample=None,
                cycle=False,
                explored_states=len(seen),
                note=f"search bound {max_states} exceeded; verdict left pending",
            )
        state = frontier.popleft()
        succ = system.transitions(state)
        edges[state] = succ
        for t in succ:
            if t.target not in seen:
                seen.add(t.target)
                frontier.append(t.target)

    terminals_reached = seen & system.terminals
    may_complete = bool(terminals_reached)

    blocked = frozenset(
        s for s in seen if s not in system.terminals and not edges.get(s)
    )

    # --- fair-liveness on the non-terminal subgraph -------------------------
    non_terminal = seen - system.terminals
    sccs = _tarjan_sccs(non_terminal, lambda s: (t.target for t in edges.get(s, ())
                                                 if t.target in non_terminal))
    fair_live_states: set[str] = set()
    for scc in sccs:
        if _is_fair_live(system, scc):
            fair_live_states.update(scc)

    # States that can reach a fair-live SCC without leaving the non-terminal
    # subgraph can avoid terminal states forever along a fair path.
    can_avoid: set[str] = set(fair_live_states)
    changed = True
    while changed:
        changed = False
        for s in non_terminal:
            if s in can_avoid:
                continue
            if any(t.target in can_avoid for t in edges.get(s, ())):
                can_avoid.add(s)
                changed = True

    must_complete_fair = not blocked and start not in can_avoid

    # --- verdict + counterexample -------------------------------------------
    if blocked:
        witness = sorted(blocked)[0]
        return CheckResult(
            status=Status.BLOCKED,
            may_complete=may_complete,
            must_complete_fair=False,
            blocked_states=blocked,
            counterexample=_path_to(edges, start, witness),
            cycle=False,
            explored_states=len(seen),
            note=f"non-terminal dead end {witness!r} is reachable",
        )
    if not may_complete:
        return CheckResult(
            status=Status.BLOCKED,
            may_complete=False,
            must_complete_fair=False,
            blocked_states=blocked,
            counterexample=None,
            cycle=False,
            explored_states=len(seen),
            note="no terminal state is reachable at all",
        )
    if must_complete_fair:
        return CheckResult(
            status=Status.GUARANTEED,
            may_complete=True,
            must_complete_fair=True,
            blocked_states=blocked,
            counterexample=None,
            cycle=False,
            explored_states=len(seen),
            note="every fair path reaches a terminal state",
        )

    # possible-only: build a replayable witness into a fair-live cycle.
    entry = _nearest(edges, start, fair_live_states, non_terminal)
    prefix = _path_to(edges, start, entry, non_terminal)
    cycle = _cycle_through(edges, entry, non_terminal)
    return CheckResult(
        status=Status.POSSIBLE_ONLY,
        may_complete=True,
        must_complete_fair=False,
        blocked_states=blocked,
        counterexample=prefix + cycle,
        cycle=True,
        explored_states=len(seen),
        note="a fair cycle avoids all terminal states; the cycle suffix may repeat forever",
    )


# --- graph helpers -----------------------------------------------------------

def _tarjan_sccs(nodes, succ) -> list[list[str]]:
    """Iterative Tarjan SCC decomposition."""
    index_of: dict[str, int] = {}
    low: dict[str, int] = {}
    on_stack: set[str] = set()
    stack: list[str] = []
    sccs: list[list[str]] = []
    for root in nodes:
        if root in index_of:
            continue
        index_of[root] = low[root] = len(index_of)
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(succ(root)))]
        while work:
            node, it = work[-1]
            descended = False
            for nxt in it:
                if nxt not in index_of:
                    index_of[nxt] = low[nxt] = len(index_of)
                    stack.append(nxt)
                    on_stack.add(nxt)
                    work.append((nxt, iter(succ(nxt))))
                    descended = True
                    break
                if nxt in on_stack:
                    low[node] = min(low[node], index_of[nxt])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                low[parent] = min(low[parent], low[node])
            if low[node] == index_of[node]:
                scc = []
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    scc.append(w)
                    if w == node:
                        break
                sccs.append(scc)
    return sccs


def _is_fair_live(system: TransitionSystem, scc: list[str]) -> bool:
    """An SCC supports an infinite *fair* run iff it has a cycle and every
    fair action enabled inside it can also be taken inside it (weak fairness)."""
    members = set(scc)
    has_cycle = len(scc) > 1 or any(
        t.target == u for u in scc for t in system.transitions(u)
    )
    if not has_cycle:
        return False
    for u in scc:
        trans = system.transitions(u)
        fair_enabled = {t.action for t in trans if t.fair}
        taken_inside = {t.action for t in trans if t.target in members}
        if not fair_enabled <= taken_inside:
            return False
    return True


def _nearest(edges, start, targets: set[str], allowed: set[str]) -> str:
    frontier = deque([start])
    seen = {start}
    while frontier:
        s = frontier.popleft()
        if s in targets:
            return s
        for t in edges.get(s, ()):
            if t.target in allowed and t.target not in seen:
                seen.add(t.target)
                frontier.append(t.target)
    raise ValueError("no target reachable")


def _path_to(edges, start, goal, allowed: set[str] | None = None) -> tuple[Step, ...]:
    """BFS shortest path as replayable Steps, optionally confined to `allowed`."""
    if start == goal:
        return ()
    parent: dict[str, tuple[str, str]] = {}  # state -> (action, predecessor)
    frontier = deque([start])
    while frontier:
        s = frontier.popleft()
        for t in edges.get(s, ()):
            if t.target == start or t.target in parent:
                continue
            if allowed is not None and t.target not in allowed:
                continue
            parent[t.target] = (t.action, s)
            if t.target == goal:
                steps = []
                node = goal
                while node != start:
                    action, prev = parent[node]
                    steps.append(Step(action, node))
                    node = prev
                return tuple(reversed(steps))
            frontier.append(t.target)
    raise ValueError(f"{goal!r} not reachable from {start!r}")


def _cycle_through(edges, entry, allowed: set[str]) -> tuple[Step, ...]:
    """A cycle from `entry` back to `entry`, staying inside `allowed`."""
    for t in edges.get(entry, ()):
        if t.target == entry:
            return (Step(t.action, entry),)
    # BFS from each successor back to entry.
    parent: dict[str, tuple[str, str]] = {}
    frontier = deque()
    for t in edges.get(entry, ()):
        if t.target in allowed and t.target not in parent:
            parent[t.target] = (t.action, entry)
            frontier.append(t.target)
    while frontier:
        s = frontier.popleft()
        for t in edges.get(s, ()):
            if t.target == entry:
                steps = [Step(t.action, entry)]
                node = s
                while node != entry:
                    action, prev = parent[node]
                    steps.append(Step(action, node))
                    node = prev
                return tuple(reversed(steps))
            if t.target in allowed and t.target not in parent:
                parent[t.target] = (t.action, s)
                frontier.append(t.target)
    raise ValueError(f"no cycle through {entry!r}")
