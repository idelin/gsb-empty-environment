"""Resource limits and the shared step budget."""

from dataclasses import dataclass

from .errors import BudgetExhausted

# Integers with absolute value above this are rejected.  This keeps
# bignum arithmetic from becoming a memory/CPU denial of service.
MAX_INT = 2 ** 62


@dataclass(frozen=True)
class Limits:
    """Hard limits enforced by the executor."""

    max_steps: int = 100_000          # default budget for one execution
    max_source: int = 100_000         # source size in UTF-8 bytes
    max_string: int = 10_000          # max characters in any string value
    max_collection: int = 10_000      # max items in any list value
    max_depth: int = 100              # max expression / value nesting
    max_number_digits: int = 64       # max digits in a number literal


class Budget:
    """A shared, atomic step budget.

    Parsing, validation, evaluation and builtin functions all draw from
    the same budget.  ``consume`` is all-or-nothing: when the remaining
    budget is insufficient nothing is consumed and BudgetExhausted is
    raised *before* any allocation or further work happens.
    """

    __slots__ = ("total", "used")

    def __init__(self, total):
        if total < 0:
            raise ValueError("budget total must be >= 0")
        self.total = total
        self.used = 0

    @property
    def remaining(self):
        return self.total - self.used

    def consume(self, amount, what="step"):
        if amount < 0:
            raise ValueError("cannot consume a negative amount")
        if amount > self.total - self.used:
            raise BudgetExhausted("step budget exhausted during %s" % what)
        self.used += amount
