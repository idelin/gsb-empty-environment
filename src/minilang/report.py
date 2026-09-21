from __future__ import annotations

from .common import Span, SyntaxErr, TypeInferenceError


def _line_col(source: bytes, pos: int) -> tuple[int, int, bytes, int]:
    """Return 1-based (line, column), the line bytes, and line start byte offset."""
    line_start = source.rfind(b"\n", 0, pos) + 1
    line_end = source.find(b"\n", pos)
    if line_end == -1:
        line_end = len(source)
    line = source[line_start:line_end]
    line_no = source.count(b"\n", 0, pos) + 1
    col_no = pos - line_start + 1  # byte-based column
    return line_no, col_no, line, line_start


def render_location(source: bytes, span: Span, message: str) -> str:
    line_no, col_no, line, line_start = _line_col(source, span.start)
    end = min(span.end, line_start + len(line))
    width = max(1, end - span.start)
    shown = line.decode("utf-8", errors="replace")
    caret_col = span.start - line_start  # 0-based in bytes
    pad = " " * len(str(line_no))
    out = [
        f"line {line_no}, bytes {span.start}..{span.end} (byte-column {col_no}): {message}",
        f"{pad} |",
        f"{line_no} | {shown}",
        f"{pad} | {' ' * caret_col}{'^' * width}",
    ]
    return "\n".join(out)


def render_type_error(source: bytes, err: TypeInferenceError) -> str:
    blocks = [f"type error: {err.summary}"]
    for index, (span, description) in enumerate(err.links, start=1):
        label = f"({index}) {description}"
        blocks.append(render_location(source, span, label))
    return "\n".join(blocks)


def render_syntax_error(source: bytes, err: SyntaxErr) -> str:
    span = Span(err.pos, min(err.pos + 1, len(source)), "the unexpected token")
    return "syntax error:\n" + render_location(source, span, str(err))
