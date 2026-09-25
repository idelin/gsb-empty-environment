"""Shared step budget and resource limits.

A single :class:`Budget` instance is threaded through lexing, parsing,
validation, lazy evaluation and every builtin / registered function, so
no stage can bypass the total quota.
"""

from __future__ import annotations

from dataclasses import dataclass

from .errors import BudgetExhausted


@dataclass(frozen=True)
class Limits:
    """Hard resource limits enforced by the executor."""

    max_steps: int = 10_000
    max_depth: int = 50
    max_string: int = 10_000
    max_collection: int = 1_000
    max_int_digits: int = 1_000
    max_reentry: int = 4


class Budget:
    """A monotone counter of consumed steps.

    ``charge`` raises :class:`BudgetExhausted` as soon as the total is
    exceeded; the exception propagates out of the executor unchanged.
    """

    __slots__ = ("max_steps", "consumed")

    def __init__(self, max_steps: int):
        self.max_steps = max_steps
        self.consumed = 0

    @property
    def remaining(self) -> int:
        return self.max_steps - self.consumed

    def charge(self, n: int = 1, what: str = "work") -> None:
        if n <= 0:
            return
        self.consumed += n
        if self.consumed > self.max_steps:
            raise BudgetExhausted(
                f"step budget of {self.max_steps} exhausted while {what}"
            )
