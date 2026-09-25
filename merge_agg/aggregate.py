"""Aggregate accumulators with exact integer arithmetic.

Semantics:

* nulls (``None``) are skipped by every aggregate;
* ``NaN`` is a real value: it is counted by ``count`` and propagates to
  the result of ``sum``/``min``/``max``/``avg``;
* ``sum``/``avg`` over an ``int`` column stay in integers (exact,
  arbitrary precision); ``avg`` of ints yields an exact
  :class:`fractions.Fraction`;
* an aggregate over zero non-null values yields ``None`` (SQL-style),
  except ``count`` which yields 0.
"""

from __future__ import annotations

from fractions import Fraction

from .batch import is_nan

FUNCS = ("sum", "count", "min", "max", "avg")


class Accumulator:
    """Running state of one aggregate over one column of one group."""

    def __init__(self, func, col_type):
        self.func = func
        self.col_type = col_type
        self.count = 0        # non-null values seen
        self.total = None     # sum/avg running total (int stays int)
        self.extreme = None   # min/max running value
        self.saw_nan = False

    def add(self, value):
        if value is None:
            return  # null is not a value
        self.count += 1
        if is_nan(value):
            self.saw_nan = True
            return
        if self.func in ("sum", "avg"):
            if self.col_type == "float":
                value = float(value)
            self.total = value if self.total is None else self.total + value
        elif self.func == "min":
            if self.extreme is None or value < self.extreme:
                self.extreme = value
        elif self.func == "max":
            if self.extreme is None or value > self.extreme:
                self.extreme = value
        # "count" needs no extra state

    def result(self):
        if self.func == "count":
            return self.count
        if self.count == 0:
            return None
        if self.saw_nan:
            return float("nan")
        if self.func == "sum":
            return self.total
        if self.func in ("min", "max"):
            return self.extreme
        if self.func == "avg":
            if self.col_type == "int":
                return Fraction(self.total, self.count)  # exact
            return self.total / self.count
        raise AssertionError(f"unknown func {self.func!r}")
