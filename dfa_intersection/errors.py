"""Exception types used by the intersection toolkit."""


class AutomataError(Exception):
    """Base error for this package."""


class CheckpointError(AutomataError):
    """Raised when a resumable checkpoint no longer matches the automata."""


class UnsupportedModeError(AutomataError):
    """Raised when an operation is unavailable in the active counting mode."""
