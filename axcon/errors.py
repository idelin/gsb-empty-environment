"""Exception types for the axcon axis-label contraction planner."""


class AxconError(Exception):
    """Base class for all axcon errors."""


class ValidationError(AxconError):
    """Raised when tensor specs, coordinates, or axis labels are invalid."""


class BudgetExceeded(AxconError):
    """Raised when a contraction step exceeds its resource budget.

    Carries the partial statistics gathered before the abort so callers can
    report how far the plan progressed. The plan is explicitly incomplete.
    """

    def __init__(self, message, stats=None):
        super().__init__(message)
        self.stats = stats


class CheckpointConflict(AxconError):
    """Raised when a checkpoint does not match the current input versions."""
