"""Pure-epsilon privacy budget accountant (sequential composition, delta = 0)."""

from __future__ import annotations

import math
from dataclasses import dataclass


class BudgetExhaustedError(RuntimeError):
    """Raised when a fresh release would exceed the total epsilon budget."""


@dataclass
class BudgetAccountant:
    total_epsilon: float
    spent_epsilon: float = 0.0

    def __post_init__(self) -> None:
        if not math.isfinite(self.total_epsilon) or self.total_epsilon <= 0:
            raise ValueError("total_epsilon must be a positive finite float")
        if self.spent_epsilon < 0:
            raise ValueError("spent_epsilon must be non-negative")

    @property
    def remaining(self) -> float:
        return self.total_epsilon - self.spent_epsilon

    def reserve(self, epsilon: float) -> None:
        if not math.isfinite(epsilon) or epsilon <= 0:
            raise ValueError("epsilon must be a positive finite float")
        if epsilon > self.remaining + 1e-15:
            raise BudgetExhaustedError(
                f"epsilon charge {epsilon} exceeds remaining budget {self.remaining:.6g} "
                f"(total {self.total_epsilon:.6g}, spent {self.spent_epsilon:.6g})"
            )
        self.spent_epsilon += epsilon

    def refund(self, epsilon: float) -> None:
        """Give back a reservation when the mechanism output was never published."""
        self.spent_epsilon = max(0.0, self.spent_epsilon - epsilon)
