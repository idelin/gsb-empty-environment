"""Explicit failure types for the bootstrap_ci package.

Every degenerate or non-estimable situation raises one of these instead of
silently returning a misleading interval.
"""


class BootstrapError(Exception):
    """Base class for all bootstrap_ci failures."""


class DegenerateJackknifeError(BootstrapError):
    """Jackknife replicates carry no information (e.g. all identical), so the
    BCa acceleration constant is not estimable."""


class BcaNotEstimableError(BootstrapError):
    """The BCa bias-correction or adjustment is not finite."""


class InvalidResampleError(BootstrapError):
    """A replicate (or the observed statistic) produced a non-finite value or
    raised, and the active invalid-policy is "raise".

    Invalid replicates are never silently dropped: dropping them would change
    the effective number of resamples and hence the achieved confidence level.
    """

    def __init__(self, message, replicate_id=None):
        self.replicate_id = replicate_id
        super().__init__(message)


class UnsupportedMethodError(BootstrapError):
    """The requested method/plan combination is not defined (explicit failure
    rather than a silent approximation)."""


class EnumerationTooLargeError(BootstrapError):
    """Exact enumeration would exceed the configured support-size budget."""
