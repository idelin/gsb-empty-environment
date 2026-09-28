"""Minimal counterexamples and deterministic replay.

``minimize`` greedily removes operations while the sub-history stays
non-linearizable, producing a 1-minimal core (removing any one more
operation makes it linearizable).  Checks that return UNKNOWN are treated
as "cannot remove", so an incompletely searched history never yields a
counterexample.

``replay_counterexample`` enumerates *every* candidate sequential
explanation of the core (pending calls optionally included) and
simulates it against the finite-state model, recording the first rejected
step.  This makes the counterexample replayable: all candidates fail, and
any found valid explanation refutes the counterexample outright.
"""
from __future__ import annotations

import itertools
from dataclasses import dataclass

from .checker import Verdict, check, predecessors
from .history import Op
from .model import QueueModel, Ret


def minimize(model: QueueModel, ops, budget: int = 100_000) -> list[Op]:
    ops = list(ops)
    verdict = check(model, ops, budget).verdict
    if verdict is not Verdict.NOT_LINEARIZABLE:
        raise ValueError(f"history verdict is {verdict.value}; nothing to minimize")

    current = list(ops)
    changed = True
    while changed:
        changed = False
        for op in list(current):
            trial = [o for o in current if o.id != op.id]
            if trial and check(model, trial, budget).verdict is Verdict.NOT_LINEARIZABLE:
                current = trial
                changed = True
                break
    return current


def is_minimal(model: QueueModel, ops, budget: int = 100_000) -> bool:
    """True iff deleting any single operation destroys the counterexample."""
    ops = list(ops)
    for op in ops:
        trial = [o for o in ops if o.id != op.id]
        if check(model, trial, budget).verdict is Verdict.NOT_LINEARIZABLE:
            return False
    return True


@dataclass(frozen=True)
class Failure:
    order: tuple[Op, ...]
    position: int
    op: Op
    state: tuple
    expected: Ret
    allowed: tuple[Ret, ...]

    def describe(self) -> str:
        seq = " -> ".join(f"{o.method}#{o.id}" for o in self.order)
        exp = "PENDING-any" if self.expected is None else str(self.expected)
        return (
            f"  candidate {seq}\n"
            f"  fails at position {self.position}: {self.op.method}#{self.op.id} "
            f"requires {exp}, but model state {self.state or '()'} only allows "
            f"{tuple(str(r) for r in self.allowed)}"
        )


def _simulate(model: QueueModel, order) -> Failure | None:
    state = model.initial()
    for position, op in enumerate(order):
        outcomes = list(model.transitions(state, op))
        matched = [s for s, ret in outcomes
                   if op.result is None or ret == op.result]
        if not matched:
            return Failure(
                order=tuple(order),
                position=position,
                op=op,
                state=state,
                expected=op.result,
                allowed=tuple(ret for _, ret in outcomes),
            )
        state = matched[0]
    return None


def _topological_orders(ops, preds):
    ops = list(ops)
    placed: set[int] = set()
    order: list[Op] = []

    def rec():
        if len(order) == len(ops):
            yield tuple(order)
            return
        for op in ops:
            if op.id not in placed and preds[op.id] <= placed:
                placed.add(op.id)
                order.append(op)
                yield from rec()
                order.pop()
                placed.discard(op.id)

    yield from rec()


@dataclass(frozen=True)
class ReplayReport:
    orders_explored: int
    failures: tuple[Failure, ...]
    truncated: bool


def replay_counterexample(model: QueueModel, ops,
                          max_orders: int = 100_000) -> ReplayReport:
    """Replay every sequential explanation; assert each one is rejected."""
    ops = list(ops)
    preds = predecessors(ops)
    done = [op for op in ops if not op.pending]
    pending = [op for op in ops if op.pending]

    failures: list[Failure] = []
    explored = 0
    truncated = False
    stop = False
    for count in range(len(pending) + 1):
        for extra in itertools.combinations(pending, count):
            subset = done + list(extra)
            for order in _topological_orders(subset, preds):
                explored += 1
                if explored > max_orders:
                    truncated = True
                    stop = True
                    break
                failure = _simulate(model, order)
                if failure is None:
                    seq = " -> ".join(f"{o.method}#{o.id}" for o in order)
                    raise ValueError(f"invalid counterexample; accepted: {seq}")
                failures.append(failure)
            if stop:
                break
        if stop:
            break
    return ReplayReport(explored, tuple(failures), truncated)


def replay_witness(model: QueueModel, linearization) -> tuple:
    """Replay a found linearization against the model; raise on any mismatch."""
    state = model.initial()
    for op in linearization:
        outcomes = list(model.transitions(state, op))
        matched = [s for s, ret in outcomes
                   if op.result is None or ret == op.result]
        if not matched:
            raise AssertionError(
                f"witness replay failed at {op}; state={state}")
        state = matched[0]
    return state
