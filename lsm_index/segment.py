"""Immutable segment + per-segment deletion bitmap (live-docs)."""

from __future__ import annotations

from dataclasses import dataclass

from .tokenizer import tokenize


@dataclass(frozen=True)
class DocRecord:
    """A document or a tombstone inside an immutable segment.

    version: monotonically increasing logical version.
    deleted: True 表示墓碑（文档删除标记），正文为空。
    """

    doc_id: str
    version: int
    deleted: bool
    text: str


class Segment:
    """An immutable on-flush/merge unit.

    文档记录与倒排表永不变；只有 live（删除位图，1=可见）会在重算遮蔽关系时
    被整体替换。Python int 不可变，旧快照持有的旧 int 不受影响（copy-on-write）。
    """

    def __init__(self, seg_id: int, records: list[DocRecord]):
        self.seg_id = seg_id
        # 同一段内 doc_id 唯一（flush/merge 均按 doc 去重后产生）
        self.records = records
        self._by_id: dict[str, DocRecord] = {}
        self._bit: dict[str, int] = {}
        self._posting_bits: dict[str, int] = {}
        bits = 0
        for bit, rec in enumerate(records):
            self._by_id[rec.doc_id] = rec
            self._bit[rec.doc_id] = bit
            bits |= 1 << bit
            if not rec.deleted:
                for term in set(tokenize(rec.text)):
                    self._posting_bits[term] = self._posting_bits.get(term, 0) | (1 << bit)
        self.base_live = bits  # 段内全部文档位
        self.live = bits       # 当前删除位图

    def has(self, doc_id: str) -> bool:
        return doc_id in self._by_id

    def get(self, doc_id: str) -> DocRecord | None:
        return self._by_id.get(doc_id)

    def bit_of(self, doc_id: str) -> int:
        return self._bit[doc_id]

    def live_doc_ids(self) -> list[str]:
        return [rec.doc_id for bit, rec in enumerate(self.records) if (self.live >> bit) & 1]

    def live_count(self) -> int:
        return self.live.bit_count()

    def term_doc_bits(self, term: str) -> int:
        """term 在本段命中的文档位（未应用删除位图）。"""
        return self._posting_bits.get(term, 0)

    def term_freq(self, term: str, live: int | None = None) -> int:
        """term 在给定删除位图可见文档中的总出现次数（统计只算可见文档）。"""
        if live is None:
            live = self.live
        hit = self._posting_bits.get(term, 0) & live
        if not hit:
            return 0
        total = 0
        while hit:
            low = hit & -hit
            bit = low.bit_length() - 1
            rec = self.records[bit]
            total += tokenize(rec.text).count(term)
            hit ^= low
        return total

    def __repr__(self) -> str:
        return f"Segment(id={self.seg_id}, docs={len(self.records)}, live={self.live.bit_count()})"
