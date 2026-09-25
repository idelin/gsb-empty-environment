"""Error types for the expression sandbox.

All errors raised to callers are ExprError instances.  Messages are built
only from source text and fixed strings; they never contain host file
system paths or host exception details.
"""


class ExprError(Exception):
    """An error in the guest expression.

    ``start``/``end`` are UTF-8 byte offsets into the source (half-open
    interval ``[start, end)``) or ``None`` when the error is not tied to a
    source location.
    """

    def __init__(self, message, start=None, end=None):
        super().__init__(message)
        self.message = message
        self.start = start
        self.end = end

    def __str__(self):
        if self.start is None:
            return self.message
        return "%s (bytes %d..%d)" % (self.message, self.start, self.end)


class ParseError(ExprError):
    """The source text is not a valid expression."""


class BudgetExhausted(ExprError):
    """The step budget ran out.

    This is always raised as an exception; it is never silently converted
    into a falsy value by short-circuit operators or conditionals.
    """
