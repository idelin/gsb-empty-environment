"""Error types for the call-by-need expression language.

Every error carries a source span expressed as a half-open interval of
UTF-8 byte offsets ``(start, end)`` into the original source text.
"""

from __future__ import annotations


def format_span(span: tuple[int, int]) -> str:
    return f"bytes {span[0]}..{span[1]}"


class LangError(Exception):
    """Base class for all language errors, annotated with a byte span."""

    def __init__(self, message: str, span: tuple[int, int]) -> None:
        self.span = span
        super().__init__(f"{message} at {format_span(span)}")


class LexError(LangError):
    """Raised while turning source text into tokens."""


class ParseError(LangError):
    """Raised while turning tokens into an AST."""


class EvalError(LangError):
    """Base class for runtime (forcing-time) errors."""


class DivByZeroError(EvalError):
    """Raised when a division whose divisor evaluates to zero is forced."""


class UnboundNameError(EvalError):
    """Raised when a variable has no binding in scope."""


class BlackHoleError(EvalError):
    """Raised when a thunk is forced while it is already being forced.

    ``cycle`` is the forcing chain that closed the loop, e.g.
    ``["x", "y", "x"]`` for an indirect self-reference.
    """

    def __init__(self, cycle: list[str], span: tuple[int, int]) -> None:
        self.cycle = list(cycle)
        chain = " -> ".join(self.cycle)
        super().__init__(f"black hole: cyclic dependency {chain}", span)
