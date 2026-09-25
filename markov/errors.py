"""Exception hierarchy for the markov package."""


class MarkovError(Exception):
    """Base class for all errors raised by this package."""


class ValidationError(MarkovError):
    """Invalid matrix, transition line, or distribution input."""


class MissingLineError(ValidationError):
    """A probability line is missing for one or more states."""


class LineSumError(ValidationError):
    """A probability line does not sum to one within tolerance."""


class NegativeProbabilityError(ValidationError):
    """A negative probability was supplied."""


class IndexOutOfRangeError(ValidationError):
    """A state index lies outside the declared size."""


class DistributionError(ValidationError):
    """A distribution has negative entries or does not sum to one."""


class SingularMatrixError(MarkovError):
    """A linear system could not be factorized."""


class NotConvergedError(MarkovError):
    """An iterative solve failed to meet its residual tolerance."""
