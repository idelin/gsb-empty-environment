"""Independent verification of check results against the full transition graph.

This module deliberately re-implements transition enumeration (instead of
reusing checker internals) so a bug in one path is unlikely to be mirrored.
Verification is only conclusive when the search completed; a PENDING result
stays inconclusive because the model was not fully searched.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from itertools import product

from .checker import CheckResult, Status
from .model import GlobalState, Model


def enabled_transitions(model: Model, state: GlobalState):
    """Independent re-enumeration of globally executable transitions."""
    for spec in model.actions:
        choices = []
        for pname in spec.participants:
            participant = model.participants[model.index[pname]]
            local = state[model.index[pname]]
            offers = [
                t for t in participant.transitions
                if t.source == local and t.action == spec.name
            ]
            if not offers:
                break
            choices.append((pname, offers))
        else:
            for combo in product(*(offers for _, offers in choices)):
                next_state = list(state)
                moves = []
                for (pname, _), trans in zip(choices, combo):
                    next_state[model.index[pname]] = trans.target
                    moves.append((pname, trans.source, trans.target))
                yield spec.name, tuple(moves), tuple(next_state)


def replay(model: Model, initial: GlobalState, steps) -> GlobalState:
    """Replay a counterexample, validating every step against the model."""
    state = initial
    for i, step in enumerate(steps):
        options = {
            (action, moves): next_state
            for action, moves, next_state in enabled_transitions(model, state)
        }
        key = (step.action, step.moves)
        if key not in options:
            raise ValueError(f"step {i} ({step.action}) not enabled at {state}")
        if options[key] != step.state:
            raise ValueError(
                f"step {i} lands at {options[key]}, evidence claims {step.state}"
            )
        state = step.state
    return state


def _full_bfs_distances(model: Model) -> dict[GlobalState, int]:
    dist: dict[GlobalState, int] = {}
    queue: deque[GlobalState] = deque()
    for init in model.initials:
        if init not in dist:
            dist[init] = 0
            queue.append(init)
    while queue:
        state = queue.popleft()
        if state in model.terminals:
            continue
        for _, _, next_state in enabled_transitions(model, state):
            if next_state not in dist:
                dist[next_state] = dist[state] + 1
                queue.append(next_state)
    return dist


@dataclass
class VerificationReport:
    conclusive: bool
    ok: bool
    checks: dict[str, object] = field(default_factory=dict)

    def summary(self) -> str:
        lines = [
            f"conclusive: {self.conclusive}",
            f"ok: {self.ok}",
        ]
        lines += [f"{name}: {value}" for name, value in self.checks.items()]
        return "\n".join(lines)


def verify(model: Model, result: CheckResult) -> VerificationReport:
    checks: dict[str, object] = {}

    if result.status is Status.PENDING:
        checks["search-complete"] = False
        checks["note"] = "budget exhausted; unexpanded frontier is not termination"
        return VerificationReport(conclusive=False, ok=False, checks=checks)

    dist = _full_bfs_distances(model)
    checks["all-initials-covered"] = all(init in dist for init in model.initials)

    deadlock_states = [
        s
        for s in dist
        if s not in model.terminals
        and not any(True for _ in enabled_transitions(model, s))
    ]

    if result.status is Status.NO_DEADLOCK:
        checks["full-graph-deadlock-free"] = not deadlock_states
        return VerificationReport(
            conclusive=True, ok=not deadlock_states, checks=checks
        )

    evidence = result.counterexample
    assert evidence is not None
    try:
        final = replay(model, evidence.initial, evidence.steps)
        checks["replay-valid"] = True
    except ValueError as exc:
        checks["replay-valid"] = str(exc)
        return VerificationReport(conclusive=True, ok=False, checks=checks)

    checks["evidence-initial-is-model-initial"] = evidence.initial in model.initials
    checks["final-is-deadlock"] = (
        final not in model.terminals
        and not any(True for _ in enabled_transitions(model, final))
    )
    shortest = min((dist[s] for s in deadlock_states), default=None)
    checks["shortest-length"] = shortest
    checks["evidence-length"] = len(evidence.steps)
    checks["is-shortest"] = shortest == len(evidence.steps)
    checks["diagnosis-state-matches"] = evidence.diagnosis.state == final

    ok = all(
        checks[k] is True
        for k in (
            "all-initials-covered",
            "replay-valid",
            "evidence-initial-is-model-initial",
            "final-is-deadlock",
            "is-shortest",
            "diagnosis-state-matches",
        )
    )
    return VerificationReport(conclusive=True, ok=ok, checks=checks)
