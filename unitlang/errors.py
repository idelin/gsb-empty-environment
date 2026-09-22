"""Error types. Every error carries a UTF-8 byte span into the source."""


class CalcError(Exception):
    """Base class for all language errors.

    span is a (start, end) pair of UTF-8 byte offsets into the source text.
    """

    def __init__(self, message, span):
        super().__init__(message)
        self.message = message
        self.span = span


class LexError(CalcError):
    """Raised while tokenizing."""


class ParseError(CalcError):
    """Raised while parsing."""


class DimError(CalcError):
    """Raised by the static dimensional-analysis pass (before execution)."""


class EvalError(CalcError):
    """Raised while evaluating (e.g. division by zero)."""
