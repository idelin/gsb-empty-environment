"""Source text together with UTF-8 byte-offset spans."""
from __future__ import annotations

from bisect import bisect_left
from dataclasses import dataclass


@dataclass(frozen=True)
class Span:
    """A half-open range of UTF-8 byte offsets into the source."""

    start: int
    end: int

    def __str__(self) -> str:
        return f"bytes {self.start}..{self.end}"


def merge_span(a: Span, b: Span) -> Span:
    return Span(min(a.start, b.start), max(a.end, b.end))


class Source:
    """Source text plus a char-index -> UTF-8 byte-offset map."""

    def __init__(self, text: str, name: str = "<input>"):
        self.text = text
        self.name = name
        offsets = [0]
        total = 0
        for ch in text:
            total += len(ch.encode("utf-8"))
            offsets.append(total)
        self._offsets = offsets

    def byte_span(self, char_start: int, char_end: int) -> Span:
        return Span(self._offsets[char_start], self._offsets[char_end])

    def excerpt(self, span: Span) -> str:
        lo = bisect_left(self._offsets, span.start)
        hi = bisect_left(self._offsets, span.end)
        return self.text[lo:hi]
