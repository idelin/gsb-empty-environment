"""Bounded FIFO queue as an explicit finite-state model.

The state is a tuple of enqueued values (head first).  Every operation has
exactly one legal outcome per state, so the state space is finite and the
checker can enumerate it exhaustively.
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterator


@dataclass(frozen=True)
class Ret:
    """Return value of a call: a normal result or an exception."""

    kind: str  # "ok" | "empty" | "full"
    value: Any = None

    def __str__(self) -> str:
        if self.kind == "ok":
            return "OK" if self.value is None else f"ok({self.value})"
        return self.kind.upper()  # exceptions: EMPTY / FULL


OK = Ret("ok")
EMPTY = Ret("empty")  # deq on an empty queue
FULL = Ret("full")    # enq on a full queue


@dataclass(frozen=True)
class QueueModel:
    capacity: int

    def initial(self) -> tuple:
        return ()

    def transitions(self, state: tuple, op) -> Iterator[tuple[tuple, Ret]]:
        """Yield every legal (next_state, result) pair for ``op`` in ``state``."""
        if op.method == "enq":
            if len(state) >= self.capacity:
                yield state, FULL
            else:
                yield state + (op.arg,), OK
        elif op.method == "deq":
            if not state:
                yield state, EMPTY
            else:
                yield state[1:], Ret("ok", state[0])
        else:
            raise ValueError(f"unknown method {op.method!r}")
