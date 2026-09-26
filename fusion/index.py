"""字段级倒排索引、固定快照与分页器。

原始字段分（raw score）：查询词去重后，各查询词在字段文本中的词频之和
（sum of term frequencies）。只保留至少命中一个查询词的文档。

快照（Snapshot）对某次查询的某个字段不可变，包含按 (-raw, doc_id)
确定序排序的全量 posting 及明确统计 min_raw / max_raw。归一化基准只
来自该快照（"明确统计"），分页只是它的视图，更换候选页不改变基准。

分页器（Paginator）按需取候选，cap() 暴露"剩余候选的归一化分上界"：
即当前游标处 posting 的归一化分（list 已降序）；游标耗尽则为 0.0。
bounded=False 表示该字段不提供上界（用于触发退化路径的测试）。
"""

from __future__ import annotations

from collections import Counter
from dataclasses import dataclass

from .corpus import Document
from .normalize import normalize_series
from .tokenizer import tokenize


@dataclass(frozen=True)
class Posting:
    doc_id: str
    raw: int
    norm: float


@dataclass(frozen=True)
class FieldStats:
    field: str
    min_raw: int
    max_raw: int
    doc_count: int
    query_terms: tuple[str, ...]


@dataclass(frozen=True)
class Snapshot:
    field: str
    query_terms: tuple[str, ...]
    postings: tuple[Posting, ...]
    stats: FieldStats

    def posting_map(self) -> dict[str, Posting]:
        return {p.doc_id: p for p in self.postings}


class Index:
    def __init__(self, docs: list[Document]):
        self._docs: dict[str, Document] = {}
        self._field_tokens: dict[tuple[str, str], list[str]] = {}
        for doc in docs:
            if doc.doc_id in self._docs:
                raise ValueError(f"doc_id 重复: {doc.doc_id!r}")
            self._docs[doc.doc_id] = doc
        self._snapshots: dict[tuple[str, tuple[str, ...]], Snapshot] = {}

    @property
    def doc_ids(self) -> list[str]:
        return list(self._docs)

    def get_doc(self, doc_id: str) -> Document:
        return self._docs[doc_id]

    def _tokens(self, doc_id: str, field: str) -> list[str]:
        key = (doc_id, field)
        if key not in self._field_tokens:
            doc = self._docs.get(doc_id)
            self._field_tokens[key] = (
                tokenize(doc.fields[field]) if doc and field in doc.fields else []
            )
        return self._field_tokens[key]

    def get_snapshot(self, query_terms: list[str] | tuple[str, ...], field: str) -> Snapshot:
        terms = tuple(dict.fromkeys(query_terms))
        cache_key = (field, terms)
        snap = self._snapshots.get(cache_key)
        if snap is not None:
            return snap

        scored: list[tuple[str, int]] = []
        for doc_id in self._docs:
            counts = Counter(self._tokens(doc_id, field))
            raw = sum(counts.get(t, 0) for t in terms)
            if raw > 0:
                scored.append((doc_id, raw))

        scored.sort(key=lambda x: (-x[1], x[0]))
        raws = [r for _, r in scored]
        norms = normalize_series(raws)
        postings = tuple(
            Posting(doc_id=d, raw=r, norm=n)
            for (d, r), n in zip(scored, norms)
        )
        stats = FieldStats(
            field=field,
            min_raw=min(raws) if raws else 0,
            max_raw=max(raws) if raws else 0,
            doc_count=len(raws),
            query_terms=terms,
        )
        snap = Snapshot(field=field, query_terms=terms, postings=postings, stats=stats)
        self._snapshots[cache_key] = snap
        return snap


@dataclass
class FieldPlan:
    field: str
    weight: float
    snapshot: Snapshot
    bounded: bool = True
    page_size: int = 16

    def __post_init__(self) -> None:
        if self.page_size < 1:
            raise ValueError("page_size 必须 >= 1")

    def paginator(self) -> "Paginator":
        return Paginator(self.snapshot, bounded=self.bounded, page_size=self.page_size)


class Paginator:
    """固定快照上的游标分页：不重、不漏、确定序。"""

    def __init__(self, snapshot: Snapshot, bounded: bool = True, page_size: int = 16):
        self.snapshot = snapshot
        self.bounded = bounded
        self.page_size = page_size
        self.offset = 0
        self.returned: set[str] = set()

    @property
    def exhausted(self) -> bool:
        return self.offset >= len(self.snapshot.postings)

    def head(self) -> Posting | None:
        if self.exhausted:
            return None
        return self.snapshot.postings[self.offset]

    def cap(self) -> float | None:
        """剩余候选归一化分上界；无界字段返回 None。"""
        if not self.bounded:
            return None
        head = self.head()
        return head.norm if head is not None else 0.0

    def fetch_page(self) -> list[Posting]:
        end = min(self.offset + self.page_size, len(self.snapshot.postings))
        page = list(self.snapshot.postings[self.offset:end])
        for p in page:
            if p.doc_id in self.returned:
                raise AssertionError("分页返回了重复 doc_id（违反不重不漏）")
            self.returned.add(p.doc_id)
        self.offset = end
        return page

    def fetch_remaining(self) -> list[Posting]:
        page = list(self.snapshot.postings[self.offset:])
        for p in page:
            if p.doc_id in self.returned:
                raise AssertionError("分页返回了重复 doc_id（违反不重不漏）")
            self.returned.add(p.doc_id)
        self.offset = len(self.snapshot.postings)
        return page
