"""Error types for the safe expression executor.

Every error message is constructed by this package itself.  Host object
reprs, host file paths and traceback text are never embedded, so errors
are safe to show to untrusted callers.
"""

from __future__ import annotations


class ExprError(Exception):
    """Base class for all executor errors.

    ``span`` is a ``(start, end)`` pair of UTF-8 byte offsets into the
    source string, or ``None`` when no location applies.
    """

    kind = "error"

    def __init__(self, message: str, span: tuple[int, int] | None = None):
        super().__init__(message)
        self.message = message
        self.span = span

    def __str__(self) -> str:
        if self.span is not None:
            return f"{self.kind} at bytes {self.span[0]}..{self.span[1]}: {self.message}"
        return f"{self.kind}: {self.message}"


class LexError(ExprError):
    kind = "lex error"


class ParseError(ExprError):
    kind = "parse error"


class ValidationError(ExprError):
    kind = "validation error"


class UnknownFunctionError(ValidationError):
    kind = "unknown function"


class ArityError(ValidationError):
    kind = "arity error"


class EvalError(ExprError):
    kind = "evaluation error"


class TypeMismatchError(EvalError):
    kind = "type error"


class DivisionByZeroError(EvalError):
    kind = "division by zero"


class IndexOutOfRangeError(EvalError):
    kind = "index out of range"


class HostFunctionError(EvalError):
    kind = "host function error"


class LimitError(ExprError):
    kind = "limit exceeded"


class BudgetExhausted(LimitError):
    """Raised when the shared step budget runs out.

    This is never swallowed by control flow: it is a subclass of
    ``LimitError``, not of anything the evaluator treats as a falsy
    result, and no ``except`` clause in the evaluator converts it.
    """

    kind = "budget exhausted"


class DepthLimitError(LimitError):
    kind = "depth limit exceeded"


class StringLimitError(LimitError):
    kind = "string limit exceeded"


class CollectionLimitError(LimitError):
    kind = "collection limit exceeded"


class IntegerLimitError(LimitError):
    kind = "integer limit exceeded"
