"""Exception hierarchy for tinyreduce."""


class ReductionError(Exception):
    """Base class for all tinyreduce errors."""


class AxisError(ValueError, ReductionError):
    """Invalid axis specification (out of range, duplicated, wrong type)."""


class EmptyReductionError(ValueError, ReductionError):
    """Reduction has no identity element for an empty (or fully masked) slice.

    Raised by mean/min/max.  sum never raises this: its identity is 0.0.
    """


class NonDifferentiableError(RuntimeError, ReductionError):
    """Higher-order differentiation was requested through a point where the
    first-order result is only a *subgradient* (e.g. a min/max tie).

    We never silently resolve such points (for instance by picking the first
    tied element); the caller is informed explicitly instead.
    """
