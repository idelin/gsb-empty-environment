"""Error types. Parse-time errors carry exact source positions."""


class ChronodurError(Exception):
    """Base class for all chronodur errors."""


class ParseError(ChronodurError):
    """A syntax/semantic error located at [start, end) in the source text."""

    kind = "parse error"

    def __init__(self, message, source, start, end=None):
        self.message = message
        self.source = source
        self.start = start
        self.end = start + 1 if end is None else max(end, start + 1)
        super().__init__(self.message)

    def __str__(self):
        width = max(1, self.end - self.start)
        caret = " " * self.start + "^" * width
        return (f"{self.kind}: {self.message} (column {self.start + 1})\n"
                f"  {self.source}\n  {caret}")


class BudgetExceeded(ParseError):
    """The evaluation budget (steps or nesting depth) was exhausted."""

    kind = "budget exceeded"


class DurationOverflow(ParseError):
    """A value exceeded the configured magnitude limits."""

    kind = "overflow"


class WallTimeError(ChronodurError):
    """A wall-clock time does not exist (it falls inside an offset gap)."""
