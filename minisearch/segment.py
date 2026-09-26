"""分段倒排索引。

每个 :class:`IndexSegment` 是不可变段（段内文档建段后不再追加），包含：
- ``doc_store``：doc_id -> Document；
- ``postings``：term -> 排序后的 doc_id 列表；
- ``deleted``：删除位图（set），命中即“不可见文档”，计数与检索均排除。

段内 ``iter_ranked`` 生成 *全部* 命中候选并按全局排序规范排好序，
不在段内截断 TopK —— 这样归并端过滤后才能继续从段里补足候选。
"""

from __future__ import annotations

import math
from collections.abc import Callable, Iterable, Iterator
from typing import Any

from .document import Document
from .sort import RankKey, SortField
from .tokenizer import SCORE_FIELD, tokenize


class IndexSegment:
    def __init__(self, segment_id: int, documents: Iterable[Document]):
        self.segment_id = segment_id
        self.doc_store: dict[int, Document] = {}
        self.postings: dict[str, list[int]] = {}
        self.deleted: set[int] = set()
        for doc in documents:
            if not isinstance(doc, Document):
                raise TypeError("documents 必须全部是 Document")
            if doc.doc_id in self.doc_store:
                raise ValueError(f"段 {segment_id} 内 doc_id 重复: {doc.doc_id}")
            self.doc_store[doc.doc_id] = doc
            for term in set(tokenize(doc.text)):
                self.postings.setdefault(term, []).append(doc.doc_id)
        for ids in self.postings.values():
            ids.sort()

    @property
    def live_count(self) -> int:
        return len(self.doc_store) - len(self.deleted)

    def is_deleted(self, doc_id: int) -> bool:
        return doc_id in self.deleted

    def delete(self, doc_id: int) -> bool:
        """标记删除（位图语义）；返回该文档本次是否新标记。"""
        if doc_id not in self.doc_store:
            return False
        if doc_id in self.deleted:
            return False
        self.deleted.add(doc_id)
        return True

    def restore(self, doc_id: int) -> bool:
        """撤销删除标记（测试/演示用）。"""
        if doc_id in self.deleted:
            self.deleted.remove(doc_id)
            return True
        return False

    def _live_postings(self, term: str) -> list[int]:
        return [
            doc_id
            for doc_id in self.postings.get(term, ())
            if doc_id not in self.deleted
        ]

    def _score_doc(self, doc_id: int, terms: list[str], live_df: dict[str, int]) -> float:
        counts: dict[str, int] = {}
        for token in tokenize(self.doc_store[doc_id].text):
            counts[token] = counts.get(token, 0) + 1
        score = 0.0
        for term in terms:
            tf = counts.get(term, 0)
            if tf:
                idf = math.log((self.live_count + 1) / (live_df[term] + 1)) + 1.0
                score += (1.0 + math.log(tf)) * idf
        return score

    def matching_doc_ids(self, query: str | None) -> list[int]:
        """查询词 OR 命中的可见 doc_id（已去重、升序）；query=None 表示匹配全部。"""
        if query is None:
            return sorted(
                doc_id for doc_id in self.doc_store if doc_id not in self.deleted
            )
        terms = list(dict.fromkeys(tokenize(query)))
        if not terms:
            return []
        matched: set[int] = set()
        for term in terms:
            matched.update(self._live_postings(term))
        return sorted(matched)

    def iter_ranked(
        self,
        query: str | None,
        sort_fields: tuple[SortField, ...],
    ) -> Iterator[tuple[RankKey, Document, float]]:
        """段内全部候选按全局排序规范排序后逐个产出 ``(key, doc, score)``。

        关键设计：段头（排序后最靠前的若干文档）即使被归并端的业务过滤器
        全部排除，归并端也能继续从本迭代器后续位置补足，因此这里不做 TopK 截断。
        """
        terms = list(dict.fromkeys(tokenize(query))) if query is not None else []
        live_df = {term: len(self._live_postings(term)) for term in terms}
        scored: list[tuple[RankKey, Document, float]] = []
        for doc_id in self.matching_doc_ids(query):
            doc = self.doc_store[doc_id]
            score = self._score_doc(doc_id, terms, live_df) if terms else 0.0
            values: dict[str, Any] = dict(doc.fields)
            values[SCORE_FIELD] = score
            values["doc_id"] = doc_id
            scored.append((RankKey(doc_id, values, sort_fields), doc, score))
        scored.sort(key=lambda item: item[0])
        yield from scored
