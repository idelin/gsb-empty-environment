"""Exception types for walk queries.

BudgetExhausted is deliberately distinct from NoSuchWalk: a query that
runs out of budget must never be reported as "no path exists".
"""


class WalkQueryError(Exception):
    """Base class for walk query failures."""


class NoSuchWalk(WalkQueryError):
    """The requested rank is provably outside the enumerated walk set."""


class BudgetExhausted(WalkQueryError):
    """The selection budget ran out before the query could be answered."""


class TrajectoryTooLong(WalkQueryError):
    """The materialized walk would exceed the configured output limit."""
