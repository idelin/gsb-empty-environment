"""源码与 UTF-8 字节区间的映射。

位置一律使用 **UTF-8 编码后的字节偏移**（闭开区间 [start, end)），
即使源码中含有中文等多字节字符，行/列号也按字节偏移计算。
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Span:
    """一段源码的 UTF-8 字节区间（左闭右开）。"""

    start: int
    end: int

    def merge(self, other: "Span") -> "Span":
        return Span(min(self.start, other.start), max(self.end, other.end))


class SourceMap:
    """保存 UTF-8 字节串，并在字节偏移与行列号之间换算。"""

    def __init__(self, source: str | bytes) -> None:
        self.text_bytes: bytes = (
            source.encode("utf-8") if isinstance(source, str) else bytes(source)
        )
        # 每行（以 \n 分隔）第一个字符的字节偏移。
        self.line_starts: list[int] = [0]
        for index, byte in enumerate(self.text_bytes):
            if byte == 0x0A:  # '\n'
                self.line_starts.append(index + 1)

    @property
    def text(self) -> str:
        return self.text_bytes.decode("utf-8")

    def locate(self, offset: int) -> tuple[int, int]:
        """字节偏移 -> (1 起始行号, 1 起始字节列号)。"""
        offset = max(0, min(offset, len(self.text_bytes)))
        lo, hi = 0, len(self.line_starts)
        while lo < hi:
            mid = (lo + hi) // 2
            if self.line_starts[mid] <= offset:
                lo = mid + 1
            else:
                hi = mid
        line = lo  # 插入点恰好是 1 起始行号
        return line, offset - self.line_starts[line - 1] + 1

    def slice(self, span: Span) -> str:
        return self.text_bytes[span.start : span.end].decode("utf-8")

    def line_text(self, line: int) -> str:
        if line < 1 or line > len(self.line_starts):
            return ""
        start = self.line_starts[line - 1]
        end = (
            self.line_starts[line] - 1
            if line < len(self.line_starts)
            else len(self.text_bytes)
        )
        return self.text_bytes[start:end].decode("utf-8")

    def describe(self, offset: int) -> str:
        line, column = self.locate(offset)
        return f"第{line}行第{column}字节列"

    def format_span(self, span: Span) -> str:
        """生成 ``位置 + 代码片段 + ^^^^ 指向`` 的三行诊断前缀。"""
        line, column = self.locate(span.start)
        end_line, end_column = self.locate(max(span.start, span.end - 1))
        snippet = self.line_text(line)
        if end_line == line:
            caret_count = max(1, end_column - column + 1)
        else:
            caret_count = max(1, len(snippet.encode("utf-8")) - column + 1)
        prefix = self.line_starts[line - 1]
        leading_spaces = span.start - prefix
        return (
            f"第{line}行第{column}字节列 (字节区间 {span.start}..{span.end}):\n"
            f"  {snippet}\n"
            f"  {' ' * leading_spaces}{'^' * caret_count}"
        )
