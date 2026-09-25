"""Error types for the minicoro language."""


class LangError(Exception):
    """A language-level error (syntax or runtime).

    ``span`` is a ``(start, end)`` pair of UTF-8 byte offsets into the
    source string, or ``None`` when no source location is available.
    ``value`` carries the thrown value for ``throw`` statements so that
    ``catch`` clauses can bind it.
    """

    def __init__(self, message, span=None, value=None):
        super().__init__(message)
        self.message = message
        self.span = span
        self.value = value

    def __str__(self):
        if self.span is not None:
            return "%s at bytes %d..%d" % (self.message, self.span[0], self.span[1])
        return self.message


class ReentrancyError(Exception):
    """Raised when a running coroutine is resumed/thrown/closed again."""
