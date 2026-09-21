"""Error types shared by the lexer, parser and interpreter.

Every error may carry a ``span``: a ``(start, end)`` pair of UTF-8 *byte*
offsets into the source text (half-open interval).
"""

from __future__ import annotations


class LangError(Exception):
    """Lexical, syntax or runtime error with an optional byte span."""

    def __init__(self, message: str, span: tuple[int, int] | None = None):
        super().__init__(message)
        self.message = message
        self.span = span
