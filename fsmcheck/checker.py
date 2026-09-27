"""BFS checker: shortest deadlock evidence over multiple initial states.

Key rules:
- Declared terminal states are never expanded (TERMINAL_RULE). A reachable
  declared terminal that still has executable actions yields a warning, not a
  deadlock report.
- A non-terminal state with no globally executable action is a deadlock.
- A state whose only executable actions lead back to itself is a livelock
  candidate, not a deadlock.
- Unexpanded frontier states (budget exhausted) keep the result PENDING;
  partial expansion is never treated as termination.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from enum import Enum

from .model import GlobalState, LocalTransition, Model

TERMINAL_RULE = "declared-terminal-states-are-not-expanded"


class Status(str, Enum):
    NO_DEADLOCK = "no-deadlock"
    DEADLOCK = "deadlock"
    PENDING = "pending"


@dataclass(frozen=True)
class Step:
    action: str
    moves: tuple[tuple[str, str, str], ...]
    state: GlobalState


@dataclass
class BlockedAction:
    action: str
    providers: dict[str, list[LocalTransition]]
    missing: list[str]

    def preconditions(self) -> list[str]:
        return [
            f"{party} must be in a state offering '{self.action}' "
            f"(currently offers {[f'{t.source}->{t.target}' for t in trans]})"
            for party, trans in sorted(self.providers.items())
        ]


@dataclass
class Diagnosis:
    state: GlobalState
    blocked_actions: list[BlockedAction]
    wait_for: dict[str, set[str]]
    minimal_cycle: list[str] | None
    no_provider: list[str]


@dataclass
class Counterexample:
    initial: GlobalState
    steps: list[Step]
    diagnosis: Diagnosis

    @property
    def final_state(self) -> GlobalState:
        return self.steps[-1].state if self.steps else self.initial


@dataclass
class CheckResult:
    status: Status
    counterexample: Counterexample | None
    warnings: list[str] = field(default_factory=list)
    expanded: int = 0
    frontier: list[GlobalState] = field(default_factory=list)
    initials_covered: tuple[GlobalState, ...] = ()


def _minimal_cycle(wait_for: dict[str, set[str]]) -> list[str] | None:
    best: list[str] | None = None
    for start in wait_for:
        dist = {start: 0}
        parent: dict[str, str] = {}
        queue = deque([start])
        while queue:
            node = queue.popleft()
            if best is not None and dist[node] + 1 >= len(best):
                continue
            for nxt in wait_for.get(node, ()):
                if nxt == start:
                    cycle = [node]
                    cur = node
                    while cur != start:
                        cur = parent[cur]
                        cycle.append(cur)
                    cycle.reverse()
                    cycle.append(start)
                    if best is None or len(cycle) < len(best):
                        best = cycle
                elif nxt not in dist:
                    dist[nxt] = dist[node] + 1
                    parent[nxt] = node
                    queue.append(nxt)
    return best


def diagnose(model: Model, state: GlobalState) -> Diagnosis:
    blocked: list[BlockedAction] = []
    wait_for: dict[str, set[str]] = {p.name: set() for p in model.participants}
    offers_any: dict[str, bool] = {p.name: False for p in model.participants}
    for spec in model.actions:
        providers: dict[str, list[LocalTransition]] = {}
        missing: list[str] = []
        for pname in spec.participants:
            offers = model.offers_at(state, pname, spec.name)
            if offers:
                providers[pname] = offers
                offers_any[pname] = True
            else:
                missing.append(pname)
        if missing:
            blocked.append(BlockedAction(spec.name, providers, missing))
            for party in providers:
                wait_for[party].update(missing)
    no_provider = sorted(p for p, any_offer in offers_any.items() if not any_offer)
    return Diagnosis(
        state=state,
        blocked_actions=blocked,
        wait_for=wait_for,
        minimal_cycle=_minimal_cycle(wait_for),
        no_provider=no_provider,
    )


def check(model: Model, budget: int | None = None) -> CheckResult:
    warnings: list[str] = []
    dist: dict[GlobalState, int] = {}
    parent: dict[GlobalState, tuple[GlobalState, str, tuple]] = {}
    queue: deque[GlobalState] = deque()
    for init in model.initials:
        if init not in dist:
            dist[init] = 0
            queue.append(init)

    expanded = 0
    frontier: list[GlobalState] = []
    deadlock_state: GlobalState | None = None

    while queue:
        state = queue.popleft()
        if state in model.terminals:
            if any(True for _ in model.successors(state)):
                warnings.append(
                    f"terminal-has-actions: declared terminal {state} still has "
                    f"executable actions; rule '{TERMINAL_RULE}' applies"
                )
            continue
        if budget is not None and expanded >= budget:
            frontier.append(state)
            frontier.extend(queue)
            break
        expanded += 1
        succs = list(model.successors(state))
        if not succs:
            deadlock_state = state
            break
        if all(next_state == state for _, _, next_state in succs):
            warnings.append(
                f"livelock-candidate: {state} only reaches itself via "
                f"{sorted({a for a, _, _ in succs})}"
            )
        for action, moves, next_state in succs:
            if next_state not in dist:
                dist[next_state] = dist[state] + 1
                parent[next_state] = (state, action, moves)
                queue.append(next_state)

    if deadlock_state is not None:
        steps: list[Step] = []
        cur = deadlock_state
        while cur in parent:
            prev, action, moves = parent[cur]
            steps.append(Step(action, moves, cur))
            cur = prev
        steps.reverse()
        evidence = Counterexample(
            initial=cur, steps=steps, diagnosis=diagnose(model, deadlock_state)
        )
        return CheckResult(
            status=Status.DEADLOCK,
            counterexample=evidence,
            warnings=warnings,
            expanded=expanded,
            initials_covered=model.initials,
        )

    if frontier:
        return CheckResult(
            status=Status.PENDING,
            counterexample=None,
            warnings=warnings,
            expanded=expanded,
            frontier=frontier,
            initials_covered=model.initials,
        )

    return CheckResult(
        status=Status.NO_DEADLOCK,
        counterexample=None,
        warnings=warnings,
        expanded=expanded,
        initials_covered=model.initials,
    )
