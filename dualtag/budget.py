"""Step / memory budgets with exception-safe scoping.

A budget counts arithmetic steps (``tick``) and dual-number allocations
(``alloc``).  ``budget_scope`` is a context manager that always restores the
previous budget on exit -- including when the body is cancelled by an
exception -- so a failed computation never leaks budget state into later
ones.
"""
from __future__ import annotations

from contextlib import contextmanager

from .errors import BudgetExceeded


class Budget:
    def __init__(self, max_steps=1_000_000, max_allocs=1_000_000):
        self.max_steps = max_steps
        self.max_allocs = max_allocs
        self.steps = 0
        self.allocs = 0

    def tick(self, n=1):
        self.steps += n
        if self.steps > self.max_steps:
            raise BudgetExceeded(
                f"step budget exceeded ({self.steps} > {self.max_steps})")

    def alloc(self, n=1):
        self.allocs += n
        if self.allocs > self.max_allocs:
            raise BudgetExceeded(
                f"memory budget exceeded ({self.allocs} > {self.max_allocs} allocations)")

    def __repr__(self):
        return (f"Budget(steps={self.steps}/{self.max_steps}, "
                f"allocs={self.allocs}/{self.max_allocs})")


_DEFAULT = Budget(max_steps=10**12, max_allocs=10**12)
_stack = [_DEFAULT]


def get_budget():
    """The currently active budget (innermost scope)."""
    return _stack[-1]


@contextmanager
def budget_scope(budget):
    """Run the body under ``budget``; always restore the previous one."""
    _stack.append(budget)
    try:
        yield budget
    finally:
        _stack.pop()
