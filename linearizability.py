"""Linearizability checker for small concurrent queue histories.

Given a finite-state model (a bounded FIFO queue) and a finite concurrent
history (invocation/response intervals), decide whether the history admits
a sequential explanation that

  * respects the real-time order (a completed operation precedes every
    operation invoked after its response),
  * is accepted by the model (every successful dequeue returns the value
    of a legal, still-queued enqueue; exceptions only where the model
    raises them),
  * distinguishes pending calls (invocation without response) from calls
    that returned, including calls that returned an exception.

Verdicts:
  LINEARIZABLE      -- a concrete sequential witness is produced.
  NOT_LINEARIZABLE  -- the search space was exhausted; a replayable,
                       delta-minimal counterexample can be extracted.
  UNKNOWN           -- the state budget ran out before the search
                       finished; the verdict stays undecided.

Overlapping operations may be reordered (a later-invoked call may be
linearized first); the checker never sorts by completion time alone, it
explores every interleaving compatible with the real-time order.

Python 3.11, standard library only.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from enum import Enum


# ---------------------------------------------------------------- model


class BoundedQueue:
    """Finite-state model of a bounded FIFO queue.

    States are tuples of queued values (head first).  Calls are tuples
    ``("enq", value)`` or ``("deq",)``.  Responses are tuples
    ``("ok", value_or_None)`` or ``("exc", "Full" | "Empty")``.
    """

    def __init__(self, capacity: int):
        if capacity < 1:
            raise ValueError("capacity must be >= 1")
        self.capacity = capacity

    def initial(self) -> tuple:
        return ()

    def step(self, state: tuple, call: tuple):
        """Yield ``(next_state, response)`` pairs for ``call`` in ``state``."""
        if call[0] == "enq":
            if len(state) < self.capacity:
                yield state + (call[1],), ("ok", None)
            else:
                yield state, ("exc", "Full")
        elif call[0] == "deq":
            if state:
                yield state[1:], ("ok", state[0])
            else:
                yield state, ("exc", "Empty")
        else:
            raise ValueError(f"unknown call {call!r}")


# ---------------------------------------------------------------- history


@dataclass(frozen=True)
class Op:
    """One operation of a concurrent history.

    ``t0`` is the invocation time, ``t1`` the response time.  A pending
    (uncompleted) call has ``t1 is None`` and ``response is None``; a
    completed call records both, even when it raised an exception
    (``response == ("exc", name)``).
    """

    oid: int
    call: tuple
    t0: int
    t1: int | None = None
    response: tuple | None = None

    @property
    def pending(self) -> bool:
        return self.t1 is None


class Verdict(Enum):
    LINEARIZABLE = "linearizable"
    NOT_LINEARIZABLE = "not_linearizable"
    UNKNOWN = "unknown"


@dataclass
class CheckResult:
    verdict: Verdict
    witness: list | None  # list of (Op, response) in linearization order
    explored: int         # number of search configurations visited


# ---------------------------------------------------------------- checker


def check_linearizability(
    model: BoundedQueue,
    ops: list[Op],
    max_states: int = 200_000,
) -> CheckResult:
    """Search all real-time-respecting linearizations of ``ops``.

    Pending operations may either take effect (with any response the
    model allows) or be dropped; completed operations must all appear
    with their recorded response.  Returns UNKNOWN if more than
    ``max_states`` configurations would have to be explored.
    """
    ops = list(ops)
    n = len(ops)
    if len({op.oid for op in ops}) != n:
        raise ValueError("duplicate operation ids")
    for op in ops:
        if (op.t1 is None) != (op.response is None):
            raise ValueError(f"op {op.oid}: t1 and response must both be set or both be None")

    # Real-time order: i must be linearized before j when i responded
    # no later than j was invoked.
    preds = [0] * n
    for i in range(n):
        if ops[i].t1 is None:
            continue
        for j in range(n):
            if i != j and ops[i].t1 <= ops[j].t0:
                preds[j] |= 1 << i

    completed_mask = 0
    for i, op in enumerate(ops):
        if not op.pending:
            completed_mask |= 1 << i

    memo: dict = {}
    explored = 0
    budget_exhausted = False

    def dfs(mask: int, state: tuple):
        """Return (status, suffix): status in {"yes", "no", "unknown"}."""
        nonlocal explored, budget_exhausted
        if (mask & completed_mask) == completed_mask:
            return "yes", []
        key = (mask, state)
        hit = memo.get(key)
        if hit is not None:
            return hit
        if explored >= max_states:
            budget_exhausted = True
            return "unknown", None
        explored += 1
        saw_unknown = False
        for i in range(n):
            bit = 1 << i
            if mask & bit or preds[i] & ~mask:
                continue
            op = ops[i]
            for next_state, resp in model.step(state, op.call):
                if op.response is not None and resp != op.response:
                    continue
                status, suffix = dfs(mask | bit, next_state)
                if status == "yes":
                    memo[key] = ("yes", [(i, resp)] + suffix)
                    return memo[key]
                if status == "unknown":
                    saw_unknown = True
        if saw_unknown:
            # Not a proven dead end; do not memoize.
            return "unknown", None
        memo[key] = ("no", None)
        return memo[key]

    status, witness = dfs(0, model.initial())
    if status == "yes":
        return CheckResult(
            Verdict.LINEARIZABLE,
            [(ops[i], resp) for i, resp in witness],
            explored,
        )
    if status == "unknown" or budget_exhausted:
        return CheckResult(Verdict.UNKNOWN, None, explored)
    return CheckResult(Verdict.NOT_LINEARIZABLE, None, explored)


# ------------------------------------------------------- counterexample


def _value_closed(ops: list[Op]) -> bool:
    """True if every successful dequeue value is justified by an enqueue
    present in the same (sub)history, accounting for multiplicities."""
    available: dict = {}
    for op in ops:
        if op.call[0] == "enq":
            available[op.call[1]] = available.get(op.call[1], 0) + 1
    for op in ops:
        if op.call[0] == "deq" and op.response is not None and op.response[0] == "ok":
            value = op.response[1]
            if available.get(value, 0) <= 0:
                return False
            available[value] -= 1
    return True


def minimize_counterexample(
    model: BoundedQueue,
    ops: list[Op],
    max_states: int = 200_000,
) -> list[Op]:
    """Delta-minimize a non-linearizable history.

    Greedily drops operations while the history stays non-linearizable.
    The result is 1-minimal: removing any single remaining operation
    makes it linearizable (or the verdict is no longer provable within
    the budget, in which case the operation is kept).

    When the history is value-closed (every dequeued value has a
    justifying enqueue), only value-closed sub-histories are considered,
    so the minimizer isolates ordering/exception bugs instead of
    collapsing to a single unjustified dequeue.  Otherwise it falls back
    to plain delta-minimization, which pinpoints the dequeue whose value
    no legal enqueue produced.
    """
    current = list(ops)
    result = check_linearizability(model, current, max_states)
    if result.verdict is not Verdict.NOT_LINEARIZABLE:
        raise ValueError(f"expected a counterexample, got {result.verdict.value}")
    keep_closed = _value_closed(current)
    changed = True
    while changed:
        changed = False
        for op in list(current):
            trial = [o for o in current if o.oid != op.oid]
            if keep_closed and not _value_closed(trial):
                continue
            trial_result = check_linearizability(model, trial, max_states)
            if trial_result.verdict is Verdict.NOT_LINEARIZABLE:
                current = trial
                changed = True
    return current


# ------------------------------------------------------------- replay


def history_to_json(model: BoundedQueue, ops: list[Op]) -> str:
    """Serialize a history so a verdict can be replayed later."""
    return json.dumps(
        {
            "model": {"type": "bounded-queue", "capacity": model.capacity},
            "ops": [
                {
                    "oid": op.oid,
                    "call": list(op.call),
                    "t0": op.t0,
                    "t1": op.t1,
                    "response": list(op.response) if op.response is not None else None,
                }
                for op in ops
            ],
        },
        indent=2,
        sort_keys=True,
    )


def history_from_json(text: str) -> tuple[BoundedQueue, list[Op]]:
    data = json.loads(text)
    spec = data["model"]
    if spec["type"] != "bounded-queue":
        raise ValueError(f"unknown model {spec['type']!r}")
    model = BoundedQueue(spec["capacity"])
    ops = [
        Op(
            oid=o["oid"],
            call=tuple(o["call"]),
            t0=o["t0"],
            t1=o["t1"],
            response=tuple(o["response"]) if o["response"] is not None else None,
        )
        for o in data["ops"]
    ]
    return model, ops


def replay(text: str, max_states: int = 200_000) -> CheckResult:
    """Re-check a serialized history; a counterexample stays reproducible."""
    model, ops = history_from_json(text)
    return check_linearizability(model, ops, max_states)


# ------------------------------------------------------------- printing


def format_call(call: tuple) -> str:
    return f"enq({call[1]!r})" if call[0] == "enq" else "deq()"


def format_response(resp: tuple) -> str:
    if resp[0] == "exc":
        return f"raise {resp[1]}"
    return "ok" if resp[1] is None else f"ok -> {resp[1]!r}"


def format_witness(witness: list) -> str:
    lines = []
    for pos, (op, resp) in enumerate(witness):
        tag = " [pending, took effect]" if op.pending else ""
        lines.append(
            f"  {pos}: op{op.oid} {format_call(op.call)} -> {format_response(resp)}{tag}"
        )
    return "\n".join(lines)


# ---------------------------------------------------------------- demo


def _demo() -> None:
    print("=== sample 1: overlapping ops, legal reorder (linearizable) ===")
    model = BoundedQueue(capacity=4)
    ops = [
        Op(0, ("enq", "A"), t0=0, t1=5, response=("ok", None)),  # slow enqueue
        Op(1, ("enq", "B"), t0=1, t1=2, response=("ok", None)),
        Op(2, ("deq",), t0=3, t1=4, response=("ok", "B")),
    ]
    result = check_linearizability(model, ops)
    print(f"verdict: {result.verdict.value} (explored {result.explored} states)")
    print("witness (enq B linearized before the earlier-invoked enq A):")
    print(format_witness(result.witness))

    print()
    print("=== sample 2: FIFO violation (not linearizable, minimal counterexample) ===")
    model = BoundedQueue(capacity=4)
    ops = [
        Op(0, ("enq", "A"), t0=0, t1=1, response=("ok", None)),
        Op(1, ("enq", "B"), t0=2, t1=3, response=("ok", None)),
        Op(2, ("deq",), t0=4, t1=5, response=("ok", "B")),  # should have been A
        Op(3, ("enq", "C"), t0=6, t1=7, response=("ok", None)),  # noise, unrelated
        Op(4, ("deq",), t0=8, t1=9, response=("ok", "A")),  # noise, unrelated
    ]
    result = check_linearizability(model, ops)
    print(f"verdict: {result.verdict.value} (explored {result.explored} states)")
    minimal = minimize_counterexample(model, ops)
    print(f"minimal counterexample keeps ops {[op.oid for op in minimal]} "
          f"(dropped noise ops)")
    blob = history_to_json(model, minimal)
    print("replayable counterexample (JSON):")
    print(blob)
    replayed = replay(blob)
    print(f"replay verdict: {replayed.verdict.value}")

    print()
    print("=== sample 3: pending call vs exception return ===")
    model = BoundedQueue(capacity=1)
    ops = [
        Op(0, ("enq", "X"), t0=0),                          # pending: may take effect
        Op(1, ("deq",), t0=1, t1=2, response=("ok", "X")),  # sees the pending enqueue
        Op(2, ("deq",), t0=3, t1=4, response=("exc", "Empty")),  # exception, not pending
    ]
    result = check_linearizability(model, ops)
    print(f"verdict: {result.verdict.value} (explored {result.explored} states)")
    print(format_witness(result.witness))

    print()
    print("=== sample 4: search budget exhausted (verdict stays undecided) ===")
    model = BoundedQueue(capacity=8)
    ops = [
        Op(i, ("enq", i), t0=2 * i, t1=2 * i + 1, response=("ok", None)) for i in range(8)
    ] + [
        Op(8 + i, ("deq",), t0=16 + 2 * i, t1=17 + 2 * i, response=("ok", i))
        for i in range(8)
    ]
    result = check_linearizability(model, ops, max_states=3)
    print(f"verdict: {result.verdict.value} (explored {result.explored} states, budget 3)")


if __name__ == "__main__":
    _demo()
