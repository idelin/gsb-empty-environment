"""分块存储的位置表与解码统计。

位置表按固定大小的块（block）存储。查询侧通过块首位置做二分“跳跃”，
只解码与目标窗口重叠的块，避免对超高频词的全量扫描；每解码一个块
都会在 DecodeStats 中计数，用于核验“按位置跳跃”确实生效。
"""
from __future__ import annotations

import bisect
from dataclasses import dataclass, field

BLOCK_SIZE = 8


@dataclass
class DecodeStats:
    """一次查询的解码与跳跃统计。"""

    blocks_decoded: int = 0
    doc_skips: int = 0
    positions_scanned: int = 0
    per_term_blocks: dict = field(default_factory=dict)

    def note_block(self, term):
        self.blocks_decoded += 1
        self.per_term_blocks[term] = self.per_term_blocks.get(term, 0) + 1


class FieldPostings:
    """某个 (term, doc, field) 的位置表，按块存储、按块惰性解码。"""

    __slots__ = ("term", "blocks", "starts", "count")

    def __init__(self, term, positions):
        if sorted(positions) != positions:
            raise ValueError("positions must be sorted")
        self.term = term
        self.blocks = tuple(
            tuple(positions[i : i + BLOCK_SIZE])
            for i in range(0, len(positions), BLOCK_SIZE)
        )
        self.starts = tuple(b[0] for b in self.blocks)
        self.count = len(positions)

    def _decode(self, block_idx, stats):
        if stats is not None:
            stats.note_block(self.term)
        return self.blocks[block_idx]

    def iter_range(self, lo, hi, stats):
        """产出落在 [lo, hi] 内的位置；只解码与窗口重叠的块。"""
        if not self.blocks:
            return
        idx = bisect.bisect_right(self.starts, lo) - 1
        if idx < 0:
            idx = 0
        while idx < len(self.blocks):
            if hi is not None and self.starts[idx] > hi:
                break
            block = self._decode(idx, stats)
            for pos in block:
                if pos < lo:
                    continue
                if hi is not None and pos > hi:
                    return
                if stats is not None:
                    stats.positions_scanned += 1
                yield pos
            idx += 1

    def contains(self, pos, stats):
        """判断 pos 是否出现；最多解码一个块。"""
        if not self.blocks:
            return False
        idx = bisect.bisect_right(self.starts, pos) - 1
        if idx < 0:
            return False
        block = self._decode(idx, stats)
        return pos in block

    def total_blocks(self):
        return len(self.blocks)

