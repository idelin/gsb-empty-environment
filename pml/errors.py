from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Span:
    start: int
    end: int


class CompileError(Exception):
    def __init__(self, diagnostics: list[str]):
        super().__init__("\n".join(diagnostics))
        self.diagnostics = diagnostics


def render_diagnostic(source: bytes, span: Span, message: str, filename: str = "<input>") -> str:
    before = source[:span.start]
    line_start = before.rfind(b"\n") + 1
    line_end = source.find(b"\n", span.end)
    if line_end < 0:
        line_end = len(source)
    line_number = before.count(b"\n") + 1
    column = span.start - line_start + 1
    line_text = source[line_start:line_end].decode("utf-8", errors="replace")
    caret_width = max(1, span.end - span.start)
    return (
        f"{filename}:{line_number}:{column}: error: {message}\n"
        f"{line_text}\n"
        f"{' ' * (column - 1)}{'^' * caret_width}"
    )
