"""位图索引：以 Python int 作为不可变位图，bit i 对应内部槽位 i 的文档。

* 每个词项、每个 (分面, 值)、每个分面的“缺失”都各持有一个位图；
* 同一文档同一值只置一个 bit —— “同文档同值只计一次”由位图天然保证；
* int 不可变：更新时重绑新 int，旧快照持有的位图引用不受影响，
  因而历史快照无需深拷贝位图即可保持一致的只读视图；
* 计数全程只做位图 AND / OR / bit_count，绝不逐文档重扫全库。
"""

from __future__ import annotations

from .documents import Doc
from .tokenizer import tokenize


class FacetIndex:
    def __init__(self) -> None:
        self.slots: list[str | None] = []  # 槽位 -> 文档身份（doc_id）
        self.slot_of: dict[str, int] = {}
        self.alive: int = 0  # 未删除文档位图
        self.terms: dict[str, int] = {}  # 词项 -> 位图
        self.facets: dict[str, dict[str, int]] = {}  # 分面 -> 值 -> 位图
        self.missing: dict[str, int] = {}  # 分面 -> 缺失该分面的文档位图
        self.version: int = 0  # 每次增删改 +1，用于标识快照代际

    @property
    def full_mask(self) -> int:
        return (1 << len(self.slots)) - 1

    def add_or_update(self, doc: Doc) -> None:
        """增量更新：同 doc_id 先清除旧位再按新内容重建，文档身份不变。"""
        if doc.doc_id in self.slot_of:
            self.remove(doc.doc_id)
        slot = len(self.slots)
        self.slots.append(doc.doc_id)
        self.slot_of[doc.doc_id] = slot
        bit = 1 << slot
        self.alive |= bit
        for token in set(tokenize(doc.text)):
            self.terms[token] = self.terms.get(token, 0) | bit
        for facet, bitmap in self.missing.items():
            if facet not in doc.facets:
                self.missing[facet] = bitmap | bit
        for facet, values in doc.facets.items():
            if facet not in self.missing:
                # 分面首次出现：此前所有存活文档都缺失该分面。
                self.missing[facet] = self.alive & ~bit
            postings = self.facets.setdefault(facet, {})
            for value in set(values):  # 重复标签去重
                postings[value] = postings.get(value, 0) | bit
        self.version += 1

    def remove(self, doc_id: str) -> None:
        slot = self.slot_of.pop(doc_id)
        self.slots[slot] = None
        keep = ~(1 << slot)
        self.alive &= keep
        for term in list(self.terms):
            self.terms[term] &= keep
        for postings in self.facets.values():
            for value in list(postings):
                postings[value] &= keep
        for facet in list(self.missing):
            self.missing[facet] &= keep
        self.version += 1

    def snapshot(self) -> "Snapshot":
        return Snapshot(self)


class Snapshot:
    """历史快照：浅拷贝字典（int 位图不可变，故快照与索引后续更新隔离）。"""

    def __init__(self, index: FacetIndex) -> None:
        self.alive = index.alive
        self.terms = dict(index.terms)
        self.facets = {facet: dict(postings) for facet, postings in index.facets.items()}
        self.missing = dict(index.missing)
        self.full_mask = index.full_mask
        self.version = index.version
        self.slot_count = len(index.slots)


def build_index(docs: list[Doc]) -> FacetIndex:
    index = FacetIndex()
    for doc in docs:
        index.add_or_update(doc)
    return index
