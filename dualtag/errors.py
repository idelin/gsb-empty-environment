"""Exception hierarchy for the dualtag package."""


class ADError(Exception):
    """Base class for all dualtag errors."""


class DomainError(ADError):
    """A function was evaluated outside its mathematical domain."""


class NonDifferentiableError(ADError):
    """The computation hit a non-smooth point (kink, tie, or branch boundary)."""


class BudgetExceeded(ADError):
    """A step or memory budget was exhausted."""

    def __init__(self, kind, limit):
        self.kind = kind
        self.limit = limit
        super().__init__(f"{kind} budget exceeded (limit={limit})")


class InterpreterError(ADError):
    """The expression interpreter rejected the input program."""


class SingularMatrixError(ADError):
    """A dense linear solve hit a (numerically) singular matrix."""


class NotConvergedError(ADError):
    """The implicit root solver did not converge; no derivative is available."""

    def __init__(self, result):
        self.result = result
        super().__init__(
            "root solve did not converge after "
            f"{result.iterations} iterations (residual={result.residual:.3e}): "
            f"{result.message}"
        )


class SingularJacobianError(ADError):
    """dF/dy is (numerically) singular at the root; the implicit derivative
    does not exist and none is returned."""

    def __init__(self, result):
        self.result = result
        super().__init__(
            f"singular jacobian at the root (|dF/dy| ~ {result.jacobian}): "
            f"{result.message}"
        )
