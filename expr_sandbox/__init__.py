"""A sandboxed expression executor.

Purely self-implemented lexer, parser and iterative evaluator.  No eval,
no compile, no host reflection.  See README.md for the language
reference and the security model.
"""

from .budget import Budget, Limits
from .errors import BudgetExhausted, ExprError, ParseError
from .executor import ExecutionResult, Executor
from .registry import Capability, FunctionRegistry

__all__ = [
    "Budget",
    "BudgetExhausted",
    "Capability",
    "ExecutionResult",
    "Executor",
    "ExprError",
    "FunctionRegistry",
    "Limits",
    "ParseError",
]
