"""dualtag: tagged dual numbers, nested/mixed AD, implicit differentiation.

Pure Python 3.11 standard library.  No autodiff or tensor dependencies.
"""
from .errors import (BudgetExceeded, DomainError, DualTagError,
                     NonConvergenceError, NonDifferentiableError,
                     SingularJacobianError)
from .budget import Budget, budget_scope, get_budget
from .dual import (Dual, Tag, abs_, cos, exp, fresh_tag, is_dual, log, max_,
                   min_, power, primal_value, sign, sin, sqrt, tags_issued,
                   tan)
from .ad import (derivative, gradient, hessian, jacobian, jvp, mixed_partial,
                 second_derivative)
from .implicit import SolveResult, implicit_jvp, solve_implicit
from .tensor import Tensor, is_singular, solve
from . import expr, numeric, symbolic

__all__ = [
    "Budget", "BudgetExceeded", "DomainError", "Dual", "DualTagError",
    "NonConvergenceError", "NonDifferentiableError", "SingularJacobianError",
    "SolveResult", "Tag", "Tensor", "abs_", "budget_scope", "cos",
    "derivative", "exp", "expr", "fresh_tag", "get_budget", "gradient",
    "hessian", "implicit_jvp", "is_dual", "is_singular", "jacobian", "jvp",
    "log", "max_", "min_", "mixed_partial", "numeric", "power",
    "primal_value", "second_derivative", "sign", "sin", "solve",
    "solve_implicit", "sqrt", "symbolic", "tags_issued", "tan",
]
