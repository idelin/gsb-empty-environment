"""Error types for the dualtag package."""


class DualTagError(Exception):
    """Base class for all dualtag errors."""


class DomainError(DualTagError, ArithmeticError):
    """Function evaluated outside its domain (e.g. log of a non-positive)."""


class NonDifferentiableError(DualTagError, ArithmeticError):
    """A non-smooth branch was hit (e.g. abs at 0, min/max at a kink)."""


class SingularJacobianError(DualTagError, ArithmeticError):
    """A Jacobian or linear system matrix is numerically singular."""


class NonConvergenceError(DualTagError, ArithmeticError):
    """Newton iteration failed to converge; no derivative is produced."""


class BudgetExceeded(DualTagError, RuntimeError):
    """Step or memory budget exhausted."""
