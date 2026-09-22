"""错误类型：所有语言错误都携带 UTF-8 字节区间 [start, end)。"""
from __future__ import annotations


class LangError(Exception):
    """语言错误，start/end 为源码的 UTF-8 字节偏移（左闭右开）。"""

    def __init__(self, message: str, start: int, end: int):
        super().__init__(message)
        self.message = message
        self.start = start
        self.end = end


def format_error(source: str, err: LangError) -> str:
    """把字节区间映射回行列，渲染带光标指示的错误信息。"""
    data = source.encode("utf-8")
    before = data[: err.start].decode("utf-8", errors="replace")
    line_no = before.count("\n")
    line = source.split("\n")[line_no]
    col = len(before) - (before.rfind("\n") + 1)
    seg = data[err.start : err.end].decode("utf-8", errors="replace")
    width = max(1, len(seg.split("\n")[0]))
    caret = " " * col + "^" * width
    return (
        f"错误：{err.message}\n"
        f"  第 {line_no + 1} 行第 {col + 1} 列（字节区间 [{err.start}, {err.end})）\n"
        f"  {line}\n"
        f"  {caret}"
    )
