"""Helpers mapping UTF-8 byte offsets to line/column positions."""
from __future__ import annotations


def line_col(source: str, byte_offset: int) -> tuple[int, int]:
    prefix = source.encode("utf-8")[:byte_offset].decode("utf-8")
    line = prefix.count("\n") + 1
    col = len(prefix.rsplit("\n", 1)[-1]) + 1
    return line, col
