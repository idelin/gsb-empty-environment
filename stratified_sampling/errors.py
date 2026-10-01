"""Shared exception types for the stratified sampling package."""


class StratifiedSamplingError(ValueError):
    """Base error for invalid sampling inputs or designs."""


class MissingStratumError(StratifiedSamplingError):
    """A stratum with positive population size has no sampled units.

    A missing stratum is never silently imputed: the design is rejected.
    """


class ZeroInclusionProbabilityError(StratifiedSamplingError):
    """A unit or stratum was given a zero inclusion probability.

    Zero inclusion probability makes design-unbiased estimation impossible,
    so it is rejected explicitly instead of being papered over.
    """
