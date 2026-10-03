"""Step and memory budgets bounding a computation.

A Budget is a context manager. While active, every arithmetic operation
ticks the step counter and every tangent slot allocated by a Dual charges
the memory counter; slots are released when the owning Dual is collected.
Exceeding either limit raises BudgetExceeded.
"""
from __future__ import annotations

import threading

from .errors import BudgetExceeded

_state = threading.local()


class Budget:
    def __init__(self, max_steps=1_000_000, max_memory=1_000_000):
        self.max_steps = max_steps
        self.max_memory = max_memory
        self.steps = 0
        self.memory = 0
        self.peak_memory = 0

    def __enter__(self):
        self._previous = getattr(_state, "current", None)
        _state.current = self
        return self

    def __exit__(self, *exc_info):
        _state.current = self._previous
        return False

    def tick(self, n=1):
        self.steps += n
        if self.steps > self.max_steps:
            raise BudgetExceeded("step", self.max_steps)

    def alloc(self, n=1):
        self.memory += n
        if self.memory > self.max_memory:
            self.memory -= n
            raise BudgetExceeded("memory", self.max_memory)
        if self.memory > self.peak_memory:
            self.peak_memory = self.memory

    def release(self, n=1):
        self.memory = max(0, self.memory - n)


def current():
    return getattr(_state, "current", None)


def tick(n=1):
    active = current()
    if active is not None:
        active.tick(n)
