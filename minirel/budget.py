"""Shared resource budgets for a single query.

Every operator in a plan charges the *same* BudgetTracker, so rows,
memory and steps are accounted exactly once at the point they are
consumed -- duplicate subplans that share a tracker cannot "forget"
to pay, and a budget breach anywhere aborts the whole query with
partial statistics intact.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Budget:
    """Limits for one query. ``None`` means unbounded."""

    max_rows: int | None = None
    max_memory: int | None = None  # bytes
    max_steps: int | None = None


class BudgetExhausted(Exception):
    """Raised the moment any dimension of the budget is exceeded."""

    def __init__(self, kind: str, used: int, limit: int) -> None:
        self.kind = kind
        self.used = used
        self.limit = limit
        super().__init__(f"{kind} budget exhausted: used {used}, limit {limit}")


class Cancelled(Exception):
    """Raised inside operators when the query is cancelled."""


class BudgetTracker:
    """Mutable, shared accounting for one query execution."""

    def __init__(self, budget: Budget) -> None:
        self.budget = budget
        self.rows = 0
        self.steps = 0
        self.memory = 0
        self.peak_memory = 0
        self._cancelled = False

    # -- accounting ------------------------------------------------------
    def charge_rows(self, n: int = 1) -> None:
        self.rows += n
        if self.budget.max_rows is not None and self.rows > self.budget.max_rows:
            raise BudgetExhausted("rows", self.rows, self.budget.max_rows)

    def charge_steps(self, n: int = 1) -> None:
        self.steps += n
        if self.budget.max_steps is not None and self.steps > self.budget.max_steps:
            raise BudgetExhausted("steps", self.steps, self.budget.max_steps)

    def charge_memory(self, n: int) -> None:
        self.memory += n
        self.peak_memory = max(self.peak_memory, self.memory)
        if self.budget.max_memory is not None and self.memory > self.budget.max_memory:
            raise BudgetExhausted("memory", self.memory, self.budget.max_memory)

    def release_memory(self, n: int) -> None:
        self.memory = max(0, self.memory - n)

    # -- cancellation ----------------------------------------------------
    def cancel(self) -> None:
        self._cancelled = True

    @property
    def cancelled(self) -> bool:
        return self._cancelled

    def check_cancelled(self) -> None:
        if self._cancelled:
            raise Cancelled("query cancelled")
