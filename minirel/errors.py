"""Execution-time errors."""


class Cancelled(Exception):
    """Raised when the cancel callback requests abortion."""
