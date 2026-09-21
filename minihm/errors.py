"""Error types carrying source spans and constraint provenance."""
from __future__ import annotations

from .source import Span


class MiniHMError(Exception):
    """Base class for every error reported by minihm."""


class LexError(MiniHMError):
    def __init__(self, message: str, span: Span):
        super().__init__(message)
        self.message = message
        self.span = span


class ParseError(MiniHMError):
    def __init__(self, message: str, span: Span):
        super().__init__(message)
        self.message = message
        self.span = span


class UnboundName(MiniHMError):
    def __init__(self, name: str, span: Span):
        super().__init__(name)
        self.name = name
        self.span = span


class TypeMismatch(MiniHMError):
    """Two type constructors could not be unified."""

    def __init__(self, left, right, reason):
        super().__init__("type mismatch")
        self.left = left
        self.right = right
        self.reason = reason


class InfiniteType(MiniHMError):
    """The occurs check rejected a recursive (infinite) type."""

    def __init__(self, var, typ, reason):
        super().__init__("infinite type")
        self.var = var
        self.typ = typ
        self.reason = reason


class RuntimeFailure(MiniHMError):
    def __init__(self, message: str, span: Span | None = None):
        super().__init__(message)
        self.message = message
        self.span = span
