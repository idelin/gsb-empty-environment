"""Depth-first model checker with partial-order reduction.

Verdicts:
* "safe"   - the whole state space was explored, no violation found.
* "unsafe" - an invariant violation or an error deadlock was found; the
             result carries a replayable counterexample trace.
* "pending"- the search hit the max_states budget before finishing; no
             conclusive claim is made.

With reduction=False the same DFS performs full enumeration, which is used
to cross-check the reduced search on small models.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .model import Model, State, format_state, state_dict
from .reducer import ReductionDecision, choose_ample, summarize_decisions

SAFE = "safe"
UNSAFE = "unsafe"
PENDING = "pending"


@dataclass
class Trace:
    """A replayable witness: states[0] is the initial state and
    actions[i] leads from states[i] to states[i+1]."""

    states: list[State]
    actions: list[str]

    def __str__(self) -> str:
        lines = [f"  s0 = {format_state(self.states[0])}"]
        for i, action in enumerate(self.actions):
            lines.append(
                f"  --{action}--> s{i + 1} = {format_state(self.states[i + 1])}"
            )
        return "\n".join(lines)


@dataclass
class CheckResult:
    model: str
    reduction: bool
    verdict: str
    states_explored: int
    transitions: int
    violation: str | None
    trace: Trace | None
    decisions: list[ReductionDecision] = field(default_factory=list)
    truncated: bool = False

    def evidence_summary(self) -> dict[str, int]:
        return summarize_decisions(self.decisions)


class _Violation(Exception):
    def __init__(self, kind: str):
        super().__init__(kind)
        self.kind = kind


class _Truncated(Exception):
    pass


def check(
    model: Model,
    reduction: bool = True,
    cycle_proviso: bool = True,
    max_states: int | None = None,
    keep_evidence: bool = True,
) -> CheckResult:
    visited: set[State] = set()
    on_stack: set[State] = set()
    decisions: list[ReductionDecision] = []
    path_states: list[State] = []
    path_actions: list[str] = []
    transitions = 0

    def dfs(state: State) -> None:
        nonlocal transitions
        visited.add(state)
        on_stack.add(state)
        path_states.append(state)

        values = state_dict(state)
        for invariant in model.invariants:
            if not invariant.holds(state):
                raise _Violation(f"invariant {invariant.name!r} violated")

        enabled = [a for a in model.actions if a.enabled(state)]
        if not enabled:
            if model.deadlock_is_error:
                raise _Violation("deadlock: no enabled actions")
            path_states.pop()
            on_stack.discard(state)
            return

        if reduction:
            chosen, decision = choose_ample(
                model, state, enabled, on_stack, cycle_proviso
            )
        else:
            chosen = enabled
            decision = ReductionDecision(
                state, [a.name for a in enabled], [a.name for a in enabled],
                "full enumeration (reduction disabled)", None, False,
            )
        if keep_evidence:
            decisions.append(decision)

        for action in chosen:
            successor = action.apply(state)
            transitions += 1
            if successor in visited:
                continue
            if max_states is not None and len(visited) >= max_states:
                raise _Truncated()
            path_actions.append(action.name)
            dfs(successor)
            path_actions.pop()

        path_states.pop()
        on_stack.discard(state)

    violation = None
    truncated = False
    try:
        dfs(model.initial_state)
    except _Violation as exc:
        violation = exc.kind
    except _Truncated:
        truncated = True

    trace = None
    if violation is not None:
        trace = Trace(states=list(path_states), actions=list(path_actions))

    if truncated:
        verdict = PENDING
    elif violation is not None:
        verdict = UNSAFE
    else:
        verdict = SAFE

    return CheckResult(
        model=model.name,
        reduction=reduction,
        verdict=verdict,
        states_explored=len(visited),
        transitions=transitions,
        violation=violation,
        trace=trace,
        decisions=decisions,
        truncated=truncated,
    )


class ReplayError(Exception):
    pass


def replay(model: Model, trace: Trace) -> State:
    """Re-execute a trace from the initial state, checking every step."""
    if len(trace.actions) != len(trace.states) - 1:
        raise ReplayError("trace length mismatch")
    by_name = {a.name: a for a in model.actions}
    state = model.initial_state
    if not trace.states or trace.states[0] != state:
        raise ReplayError("trace does not start at the initial state")
    for i, name in enumerate(trace.actions):
        action = by_name.get(name)
        if action is None:
            raise ReplayError(f"unknown action {name!r} at step {i}")
        if not action.enabled(state):
            raise ReplayError(f"action {name!r} not enabled at step {i}")
        state = action.apply(state)
        if state != trace.states[i + 1]:
            raise ReplayError(f"state mismatch after step {i}")
    return state


def validate_counterexample(model: Model, trace: Trace, violation: str) -> bool:
    """Replay a counterexample and confirm it really exhibits the violation."""
    final = replay(model, trace)
    if violation.startswith("deadlock"):
        if any(a.enabled(final) for a in model.actions):
            raise ReplayError("trace does not end in a deadlock")
        return True
    for invariant in model.invariants:
        if not invariant.holds(final):
            return True
    raise ReplayError("trace does not end in a violation")


@dataclass
class Comparison:
    model: str
    por: CheckResult
    full: CheckResult

    @property
    def conclusive(self) -> bool:
        return self.por.verdict != PENDING and self.full.verdict != PENDING

    @property
    def verdicts_agree(self) -> bool:
        return self.por.verdict == self.full.verdict


def compare_with_full(model: Model, max_states: int | None = None) -> Comparison:
    """Run reduced and full enumeration searches on the same model."""
    por = check(model, reduction=True, max_states=max_states)
    full = check(model, reduction=False, max_states=max_states)
    return Comparison(model=model.name, por=por, full=full)
