"""Exception types and source-span formatting for Rill."""

from __future__ import annotations

from dataclasses import dataclass


def line_col(source: str, offset: int) -> tuple[int, int]:
    """Return a 1-based (line, byte-column) position for a UTF-8 byte offset."""
    prefix = source.encode("utf-8")[:offset].decode("utf-8", errors="replace")
    line = prefix.count("\n") + 1
    last_nl = prefix.rfind("\n")
    col_bytes = len(prefix[last_nl + 1 :].encode("utf-8")) + 1
    return line, col_bytes


def _line_text(source: str, line: int) -> str:
    lines = source.splitlines()
    if 1 <= line <= len(lines):
        return lines[line - 1]
    return ""


@dataclass
class RillError(Exception):
    message: str
    start: int
    end: int

    def render(self, source: str, filename: str = "<input>") -> str:
        line, col = line_col(source, self.start)
        text = _line_text(source, line)
        _, col_end = line_col(source, self.end)
        width = max(1, col_end - col)
        caret_indent = len(text[: col - 1].encode("utf-8"))
        return (
            f"{filename}:{line}:{col}: {self.kind}: {self.message}\n"
            f"  {text}\n"
            f"  {' ' * caret_indent}{'^' * min(width, max(1, len(text.encode('utf-8')) - caret_indent))}"
        )


class CompileError(RillError):
    kind = "compile error"


class EvalError(RillError):
    kind = "runtime error"
