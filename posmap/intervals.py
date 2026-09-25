"""区间关系映射模型。

所有跨层映射都是"区间到区间"的关系（而非单个偏移）：
一个映射块 Block 把 src 层的一个半开区间关联到 dst 层的一个半开区间，
并携带 tag 作为来源信息。组合（compose）即关系复合：当两个块在中间层
的区间相交（非空交集）时保留 (src, dst) 对。该表示天然保留一对多、
重叠来源与生成片段（生成片段即没有对应块的区间）。
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Span:
    """半开字节区间 [start, end)，doc 标识所在文档/层。"""

    doc: str
    start: int
    end: int

    def __post_init__(self):
        if self.start < 0 or self.end < self.start:
            raise ValueError(f"非法区间: {self}")

    def __len__(self):
        return self.end - self.start

    def overlaps(self, other: "Span") -> bool:
        return (
            self.doc == other.doc
            and self.start < other.end
            and other.start < self.end
        )

    def intersection(self, other: "Span") -> "Span | None":
        if not self.overlaps(other):
            return None
        return Span(self.doc, max(self.start, other.start), min(self.end, other.end))

    def contains(self, other: "Span") -> bool:
        return (
            self.doc == other.doc
            and self.start <= other.start
            and other.end <= self.end
        )

    def shift(self, delta: int) -> "Span":
        return Span(self.doc, self.start + delta, self.end + delta)


@dataclass(frozen=True)
class Block:
    """一条映射块：src 区间与 dst 区间相关，tag 携带来源信息。"""

    src: Span
    dst: Span
    tag: tuple = ()


class IntervalMap:
    """从 src_layer 到 dst_layer 的区间关系集合。"""

    def __init__(self, src_layer: str, dst_layer: str):
        self.src_layer = src_layer
        self.dst_layer = dst_layer
        self.blocks: list[Block] = []

    def add(self, src: Span, dst: Span, tag: tuple = ()):
        self.blocks.append(Block(src, dst, tuple(tag)))

    def targets(self, span: Span) -> list[Block]:
        """src 与给定区间相交的所有块（正查）。"""
        return [b for b in self.blocks if b.src.overlaps(span)]

    def sources(self, span: Span) -> list[Block]:
        """dst 与给定区间相交的所有块（反查）。"""
        return [b for b in self.blocks if b.dst.overlaps(span)]

    def compose(self, other: "IntervalMap") -> "IntervalMap":
        """关系复合：self: A->B, other: B->C  =>  A->C。

        仅当中间层区间相交时保留 (src, dst) 对；tag 按顺序拼接，
        因此一对多与重叠来源在组合后原样保留。
        """
        if self.dst_layer != other.src_layer:
            raise ValueError(f"层不匹配: {self.dst_layer} vs {other.src_layer}")
        out = IntervalMap(self.src_layer, other.dst_layer)
        for b1 in self.blocks:
            for b2 in other.blocks:
                if b1.dst.overlaps(b2.src):
                    out.add(b1.src, b2.dst, b1.tag + b2.tag)
        return out

    def relation(self) -> set:
        return {(b.src, b.dst, b.tag) for b in self.blocks}
