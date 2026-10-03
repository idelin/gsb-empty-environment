"""dualtag: tagged dual numbers, nested forward-mode AD, a pure-expression
interpreter, and differentiable implicit root finding. Stdlib only."""

from . import dual, tensor
from .budget import Budget
from .diff import (derivative, gradient, hessian, jacobian, jvp,
                   mixed_directional, mixed_partial, second_derivative)
from .dual import Dual
from .errors import (ADError, BudgetExceeded, DomainError, InterpreterError,
                     NonDifferentiableError, NotConvergedError,
                     SingularJacobianError, SingularMatrixError)
from .implicit import (RootResult, implicit_derivative, implicit_jacobian,
                       solve_scalar, solve_system)
from .interpreter import Interpreter, compile_expression, eval_expr
from .symbolic import symbolic_derivative
from .tags import Tag, active_tags, tag_scope

__version__ = "0.1.0"
