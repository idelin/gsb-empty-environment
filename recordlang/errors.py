"""Error type carrying a UTF-8 byte-range span into the source."""

from __future__ import annotations


class LangError(Exception):
    """An error in the source program, located by a UTF-8 byte range."""

    def __init__(self, message: str, span: tuple[int, int] | None = None):
        self.message = message
        self.span = span
        if span is not None:
            super().__init__(f"{message} [bytes {span[0]}..{span[1]}]")
        else:
            super().__init__(message)
