"""Reachability checker for timed automata with integer clocks.

Model
-----
A model is a finite state machine: locations connected by transitions.
Each transition carries a guard (a conjunction of clock/integer-constant
comparisons) and a set of clocks to reset to zero.  Clocks take integer
values; time advances in integer delays.

Explicit finiteness / equivalence rules
---------------------------------------
1. Integerization: initial values are 0, resets set clocks to 0 and all
   guard constants are integers, so only integer delays need to be
   considered.  Any state reachable with a fractional delay is also
   reachable with an integer delay.
2. Time upper bound T: the global elapsed time never exceeds T, hence
   only finitely many integer time instants are explored.
3. Truncation (equivalence quotient): a clock value greater than
   CAP = max(T, max guard constant) is truncated to CAP.  Every guard
   constant is strictly smaller than CAP, so each guard evaluates
   identically for the real value and for its truncation.  The truncated
   state space (locations x valuations) is finite.
4. Zero-time loops cannot expand forever: zero-time successors are closed
   under the finite visited-state set, so a zero-time cycle revisits an
   already-seen state and is never expanded again.

Verdicts
--------
REACHABLE   - a witness trace to the target location was found; the trace
             is replayable step by step (see ``replay``).
UNREACHABLE- the whole truncated state space was explored, no target state.
UNKNOWN    - the state budget was exhausted before the search completed;
             the verdict stays pending (nothing is claimed).
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from typing import Optional, Sequence

OPS = {
    "<": lambda a, b: a < b,
    "<=": lambda a, b: a <= b,
    ">": lambda a, b: a > b,
    ">=": lambda a, b: a >= b,
    "==": lambda a, b: a == b,
}

REACHABLE = "REACHABLE"
UNREACHABLE = "UNREACHABLE"
UNKNOWN = "UNKNOWN"


@dataclass(frozen=True)
class Guard:
    """A single atomic constraint, e.g. Guard("x", "<", 3) means x < 3."""

    clock: str
    op: str
    const: int

    def __post_init__(self) -> None:
        if self.op not in OPS:
            raise ValueError(f"unsupported operator: {self.op!r}")
        if not isinstance(self.const, int):
            raise TypeError("guard constant must be an integer")

    def holds(self, valuation: dict[str, int]) -> bool:
        return OPS[self.op](valuation[self.clock], self.const)

    def __str__(self) -> str:
        return f"{self.clock}{self.op}{self.const}"


@dataclass(frozen=True)
class Transition:
    source: str
    target: str
    guard: tuple[Guard, ...] = ()
    resets: tuple[str, ...] = ()
    label: str = ""

    def __str__(self) -> str:
        parts = [self.label or f"{self.source}->{self.target}"]
        if self.guard:
            parts.append("[" + " & ".join(str(g) for g in self.guard) + "]")
        if self.resets:
            parts.append("{" + ",".join(self.resets) + "}:=0")
        return " ".join(parts)


@dataclass
class Model:
    """A timed automaton with integer clocks and a global time bound."""

    clocks: tuple[str, ...]
    initial: str
    transitions: tuple[Transition, ...]
    time_bound: int

    def __post_init__(self) -> None:
        if self.time_bound < 0:
            raise ValueError("time_bound must be >= 0")
        clock_set = set(self.clocks)
        for t in self.transitions:
            for g in t.guard:
                if g.clock not in clock_set:
                    raise ValueError(f"guard uses unknown clock {g.clock!r}")
            for r in t.resets:
                if r not in clock_set:
                    raise ValueError(f"reset of unknown clock {r!r}")
        max_const = max(
            (g.const for t in self.transitions for g in t.guard), default=0
        )
        # Truncation cap: any clock value above `cap` is stored as `cap`.
        # cap exceeds every guard constant, so guards are insensitive to
        # the truncation (equivalence quotient).
        self.cap = max(self.time_bound, max_const) + 1

    def outgoing(self, location: str) -> list[Transition]:
        return [t for t in self.transitions if t.source == location]


# A state is (location, clock values in model.clocks order, global time).
State = tuple[str, tuple[int, ...], int]


@dataclass
class Step:
    """One replayable step of a trace."""

    kind: str  # "delay" or "transition"
    detail: object  # int delay, or the Transition taken
    state: State  # state reached after this step

    def describe(self, model: Model) -> str:
        loc, values, _ = self.state
        valuation = format_valuation(model, values)
        if self.kind == "delay":
            head = f"delay {self.detail}"
        else:
            head = f"take {self.detail}"
        return f"{head}  ==>  ({loc}, {valuation})"


def format_valuation(model: Model, values: tuple[int, ...]) -> str:
    parts = []
    for name, v in zip(model.clocks, values):
        parts.append(f"{name}={v}+" if v >= model.cap else f"{name}={v}")
    return "{" + ", ".join(parts) + "}"


def format_state(model: Model, state: State) -> str:
    loc, values, _ = state
    return f"({loc}, {format_valuation(model, values)})"


class ReplayError(Exception):
    """Raised when a trace cannot be replayed on the model."""


def _truncate(model: Model, values: Sequence[int]) -> tuple[int, ...]:
    return tuple(min(v, model.cap) for v in values)


def _apply_delay(model: Model, state: State, delay: int) -> State:
    loc, values, clock_time = state
    if delay < 0 or clock_time + delay > model.time_bound:
        raise ReplayError(
            f"illegal delay {delay} at global time {clock_time} "
            f"(bound {model.time_bound})"
        )
    new_values = _truncate(model, [v + delay for v in values])
    return (loc, new_values, clock_time + delay)


def _apply_transition(model: Model, state: State, trans: Transition) -> State:
    loc, values, clock_time = state
    if trans.source != loc:
        raise ReplayError(
            f"transition {trans} not enabled at location {loc!r}"
        )
    valuation = dict(zip(model.clocks, values))
    for g in trans.guard:
        if not g.holds(valuation):
            raise ReplayError(f"guard {g} violated at {valuation}")
    reset_set = set(trans.resets)
    new_values = _truncate(
        model,
        [0 if name in reset_set else v for name, v in zip(model.clocks, values)],
    )
    return (trans.target, new_values, clock_time)


def replay(model: Model, trace: Sequence[Step]) -> State:
    """Re-execute a trace on the model, validating every step.

    Returns the final state.  Raises ReplayError if any delay is illegal,
    any guard is violated, or a recorded intermediate state does not match
    the recomputed one.  This makes every counterexample/witness
    independently checkable.
    """
    state: State = (model.initial, tuple(0 for _ in model.clocks), 0)
    for i, step in enumerate(trace):
        if step.kind == "delay":
            state = _apply_delay(model, state, int(step.detail))
        elif step.kind == "transition":
            state = _apply_transition(model, state, step.detail)
        else:
            raise ReplayError(f"step {i}: unknown kind {step.kind!r}")
        if step.state != state:
            raise ReplayError(
                f"step {i}: recorded state {step.state} does not match "
                f"replayed state {state}"
            )
    return state


@dataclass
class Result:
    status: str  # REACHABLE | UNREACHABLE | UNKNOWN
    target: str
    trace: list[Step] = field(default_factory=list)
    explored_states: int = 0
    pending_states: int = 0  # frontier size left unexplored (for UNKNOWN)

    def summary(self, model: Model) -> str:
        lines = [
            f"verdict: {self.status} (target {self.target!r})",
            f"explored states: {self.explored_states}, "
            f"pending frontier: {self.pending_states}",
        ]
        if self.status == REACHABLE:
            lines.append("witness trace (replayable):")
            lines.append(
                f"  start ==> {format_state(model, (model.initial, tuple(0 for _ in model.clocks), 0))}"
            )
            for step in self.trace:
                lines.append("  " + step.describe(model))
        return "\n".join(lines)


def check_reachability(
    model: Model,
    target: str,
    max_states: Optional[int] = None,
) -> Result:
    """Breadth-first search over the truncated integer state space.

    The search only ever explores the given finite model.  If the frontier
    is exhausted the verdict is conclusive (REACHABLE/UNREACHABLE); if the
    state budget runs out first the verdict stays UNKNOWN (pending).
    """
    start: State = (model.initial, tuple(0 for _ in model.clocks), 0)
    visited: set[State] = {start}
    queue: deque[State] = deque([start])
    parent: dict[State, tuple[State, Step]] = {}

    def build_trace(end: State) -> list[Step]:
        steps: list[Step] = []
        node = end
        while node != start:
            prev, step = parent[node]
            steps.append(step)
            node = prev
        steps.reverse()
        return steps

    explored = 0
    while queue:
        if max_states is not None and explored >= max_states:
            return Result(
                status=UNKNOWN,
                target=target,
                explored_states=explored,
                pending_states=len(queue),
            )
        state = queue.popleft()
        explored += 1
        loc, values, clock_time = state

        if loc == target:
            return Result(
                status=REACHABLE,
                target=target,
                trace=build_trace(state),
                explored_states=explored,
                pending_states=len(queue),
            )

        successors: list[tuple[str, object, State]] = []
        # Zero-time successors: fire every enabled transition.  Resets
        # discard the old clock value entirely (it is not retained).
        for trans in model.outgoing(loc):
            valuation = dict(zip(model.clocks, values))
            if all(g.holds(valuation) for g in trans.guard):
                try:
                    nxt = _apply_transition(model, state, trans)
                except ReplayError:
                    continue
                successors.append(("transition", trans, nxt))
        # Time successor: the smallest integer delay (1 time unit).
        # Larger delays are reached by chaining unit delays, so no
        # integer-reachable state is missed.
        if clock_time < model.time_bound:
            successors.append(("delay", 1, _apply_delay(model, state, 1)))

        for kind, detail, nxt in successors:
            if nxt not in visited:
                visited.add(nxt)
                parent[nxt] = (state, Step(kind=kind, detail=detail, state=nxt))
                queue.append(nxt)

    return Result(
        status=UNREACHABLE,
        target=target,
        explored_states=explored,
        pending_states=0,
    )


def demo_model() -> Model:
    """Sample model: a task must finish strictly before deadline 4.

    idle --start, {x}:=0--> run
    run  --finish [x>=2 & x<4]--> done
    run  --timeout [x>=4]------> failed
    """
    return Model(
        clocks=("x",),
        initial="idle",
        time_bound=10,
        transitions=(
            Transition("idle", "run", resets=("x",), label="start"),
            Transition(
                "run",
                "done",
                guard=(Guard("x", ">=", 2), Guard("x", "<", 4)),
                label="finish",
            ),
            Transition(
                "run", "failed", guard=(Guard("x", ">=", 4),), label="timeout"
            ),
        ),
    )


def main() -> None:
    model = demo_model()
    print("=== demo model ===")
    print(f"clocks: {model.clocks}, initial: {model.initial}, "
          f"time bound: {model.time_bound}, truncation cap: {model.cap}")
    for t in model.transitions:
        print(f"  {t.source} --{t.label or 'tau'}--> {t.target}  {t}")
    print()

    for target in ("done", "failed", "nonexistent"):
        result = check_reachability(model, target)
        print(f"--- target: {target} ---")
        print(result.summary(model))
        if result.status == REACHABLE:
            final = replay(model, result.trace)
            assert final[0] == target
            print(f"replay check: OK (replayed final state "
                  f"{format_state(model, final)})")
        print()


if __name__ == "__main__":
    main()
