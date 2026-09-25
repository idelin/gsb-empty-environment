"""Diagnostics with UTF-8 byte spans and structured fix information."""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class Diagnostic:
    message: str
    start: int  # UTF-8 byte offset
    end: int
    source: str = "parser"  # "lexer" | "parser" | "runtime"
    cost: int | None = None  # minimal edit cost, when recovery succeeded
    fixes: list = field(default_factory=list)  # one description per candidate
    truncated: bool = False  # True when the candidate list was cut by a budget

    def __str__(self):
        return f"[{self.source}] bytes {self.start}..{self.end}: {self.message}"
