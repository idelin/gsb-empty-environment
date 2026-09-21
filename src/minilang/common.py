from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Span:
    """A half-open UTF-8 byte interval [start, end) inside the source file."""

    start: int
    end: int
    note: str = ""

    def shifted(self, note: str) -> "Span":
        return Span(self.start, self.end, note)


class LangError(Exception):
    """Base class for all language diagnostics."""


class SyntaxErr(LangError):
    def __init__(self, message: str, pos: int):
        super().__init__(message)
        self.pos = pos


class TypeInferenceError(LangError):
    def __init__(self, summary: str, links: list[tuple[Span, str]], kind: str):
        super().__init__(summary)
        self.summary = summary
        # Ordered, minimal chain of constraints. Each item is (location, description).
        self.links = links
        self.kind = kind


class RuntimeErr(LangError):
    pass
