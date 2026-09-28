"""Call history: invocation / response events per operation.

Times are abstract integers.  A call whose response never arrives
(``response is None``) is *pending*; per Herlihy-Wing a pending call may
either take effect at some point after its invocation or be removed
(treated as if it never happened).
"""
from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Optional

from .model import Ret, OK, EMPTY


@dataclass(frozen=True)
class Op:
    id: int
    method: str               # "enq" | "deq"
    arg: Any                  # enqueue argument; None for deq
    result: Optional[Ret]     # None  <=>  call pending (never returned)
    invoke: int
    response: Optional[int]   # None  <=>  call pending

    @property
    def pending(self) -> bool:
        return self.response is None

    def __str__(self) -> str:
        head = f"enq({self.arg})" if self.method == "enq" else "deq()"
        end = "..." if self.response is None else str(self.response)
        text = f"{head}#{self.id} [{self.invoke}..{end}]"
        if self.pending:
            return text + " PENDING"
        return text + f" -> {self.result}"


def enq(op_id: int, arg: Any, invoke: int, response: int,
        result: Ret = OK) -> Op:
    return Op(op_id, "enq", arg, result, invoke, response)


def deq(op_id: int, invoke: int, response: int, result: Ret = EMPTY) -> Op:
    return Op(op_id, "deq", None, result, invoke, response)


def pending_enq(op_id: int, arg: Any, invoke: int) -> Op:
    return Op(op_id, "enq", arg, None, invoke, None)


def pending_deq(op_id: int, invoke: int) -> Op:
    return Op(op_id, "deq", None, None, invoke, None)
