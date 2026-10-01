"""Exception hierarchy for the factor elimination package."""


class FactorGraphError(Exception):
    """Base class for all errors raised by this package."""


class DomainConflictError(FactorGraphError, ValueError):
    """Same variable name bound to two different domains."""


class UnknownVariableError(FactorGraphError, KeyError):
    """A factor or query references a variable that was never declared."""


class InvalidEvidenceError(FactorGraphError, ValueError):
    """Evidence assigns a value outside the variable's domain."""


class ContradictoryEvidenceError(FactorGraphError):
    """Evidence has probability zero: the normalization constant is 0."""
