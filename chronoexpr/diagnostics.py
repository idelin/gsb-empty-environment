"""Error diagnostics with exact source positions."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Diagnostic:
    message: str
    line: int
    col: int
    length: int = 1

    def render(self, source: str) -> str:
        lines = source.splitlines() or [""]
        text = lines[self.line - 1] if 0 < self.line <= len(lines) else ""
        caret = " " * (self.col - 1) + "^" * max(1, self.length)
        return (
            f"error at line {self.line}, column {self.col}: {self.message}\n"
            f"{text}\n{caret}"
        )


class ChronoError(Exception):
    """Raised for every parse/compile/evaluation failure; carries a Diagnostic."""

    def __init__(self, diag: Diagnostic, source: str | None = None):
        super().__init__(diag.message)
        self.diag = diag
        self.source = source

    def __str__(self) -> str:
        if self.source is not None:
            return self.diag.render(self.source)
        return (
            f"error at line {self.diag.line}, column {self.diag.col}: "
            f"{self.diag.message}"
        )
