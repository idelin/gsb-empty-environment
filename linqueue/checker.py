"""Exhaustive linearizability search against the finite-state queue model.

Real-time rule: if call A responded before call B was invoked, A must
precede B in every candidate order.  Overlapping calls may be placed in
any order, so a later-invoked call that returns first can be reordered.

Pending calls are optional: all completed calls must be placed, while a
pending call may be dropped or placed anywhere its invocation constraints
allow.

The search is depth-first with memoisation over (model_state, placed).
If the state budget is exhausted before the space is proven empty, the
verdict stays UNKNOWN ("undecided") rather than claiming a counterexample.
"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Iterable, Optional

from .history import Op
from .model import QueueModel


class Verdict(Enum):
    LINEARIZABLE = "linearizable"
    NOT_LINEARIZABLE = "not-linearizable"
    UNKNOWN = "unknown"


@dataclass(frozen=True)
class CheckResult:
    verdict: Verdict
    linearization: Optional[tuple[Op, ...]]
    explored: int
    budget: int


class _BudgetExceeded(Exception):
    pass


def predecessors(ops: Iterable[Op]) -> dict[int, frozenset[int]]:
    """a precedes b  <=>  a responded strictly before b was invoked."""
    ops = list(ops)
    preds = {op.id: set() for op in ops}
    for a in ops:
        if a.response is None:
            continue
        for b in ops:
            if a.id != b.id and a.response < b.invoke:
                preds[b.id].add(a.id)
    return {k: frozenset(v) for k, v in preds.items()}


def check(model: QueueModel, ops: Iterable[Op],
          budget: int = 100_000) -> CheckResult:
    """Return LINEARIZABLE (+ a concrete witness order), NOT_LINEARIZABLE,
    or UNKNOWN when ``budget`` states were explored without finishing."""
    ops = list(ops)
    ids = [op.id for op in ops]
    if len(set(ids)) != len(ids):
        raise ValueError("duplicate operation ids")

    preds = predecessors(ops)
    completed = frozenset(op.id for op in ops if not op.pending)
    by_id = {op.id: op for op in ops}

    explored = 0
    memo: dict[tuple, bool] = {}
    choice: dict[tuple, tuple[int, tuple]] = {}

    def search(state: tuple, placed: frozenset[int]) -> bool:
        nonlocal explored
        if completed <= placed:  # remaining pending calls are dropped
            return True
        key = (state, placed)
        if key in memo:
            return memo[key]
        explored += 1
        if explored > budget:
            raise _BudgetExceeded

        for op in ops:
            if op.id in placed or not preds[op.id] <= placed:
                continue
            for new_state, ret in model.transitions(state, op):
                # Completed call: recorded result must match.
                # Pending call: any legal outcome is accepted.
                if op.result is not None and ret != op.result:
                    continue
                if search(new_state, placed | {op.id}):
                    memo[key] = True
                    choice[key] = (op.id, new_state)
                    return True
        memo[key] = False
        return False

    try:
        found = search(model.initial(), frozenset())
    except _BudgetExceeded:
        return CheckResult(Verdict.UNKNOWN, None, explored, budget)

    if not found:
        return CheckResult(Verdict.NOT_LINEARIZABLE, None, explored, budget)

    witness: list[Op] = []
    state = model.initial()
    placed: frozenset[int] = frozenset()
    while not completed <= placed:
        op_id, state = choice[(state, placed)]
        witness.append(by_id[op_id])
        placed = placed | {op_id}
    return CheckResult(Verdict.LINEARIZABLE, tuple(witness), explored, budget)
