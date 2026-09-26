"""索引门面：管理多个段，做多字段 TopK 的 k 路归并。

归并做法（核心正确性要点）：
1. 每个段用自己的 ``iter_ranked`` 产出该段 *全部* 命中候选（段内已按同一套
   多字段规范排序），**不在段内只取前 K 个原始项**；
2. 用一个小顶堆做 k 路归并，堆顶永远是“全局最靠前且尚未消费”的候选；
3. 弹出堆顶后立刻从其所属段拉下一个候选补堆 —— 即使某段排序最靠前的文档
   （段头）被业务过滤器全部排除，也会继续读取该段后续候选；
4. 过滤器在归并阶段施加，被过滤的候选只占用“已扫描”名额，不占用 TopK，
   因而结果始终能在剩余候选中补满 K 个。
"""

from __future__ import annotations

import heapq
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass
from typing import Any

from .document import Document
from .segment import IndexSegment
from .sort import SortField
from .tokenizer import SCORE_FIELD

DocFilter = Callable[[Document], bool]


@dataclass(frozen=True, slots=True)
class SearchHit:
    doc_id: int
    score: float
    document: Document
    segment_id: int


class Index:
    def __init__(self) -> None:
        self.segments: list[IndexSegment] = []

    def commit(self, documents: Iterable[Document]) -> IndexSegment:
        """把一批文档固化为一个新的不可变段。"""
        segment = IndexSegment(len(self.segments), documents)
        self.segments.append(segment)
        return segment

    def segment_of(self, doc_id: int) -> IndexSegment | None:
        for segment in self.segments:
            if doc_id in segment.doc_store:
                return segment
        return None

    def delete(self, doc_id: int) -> bool:
        """在所属段的删除位图上标记 doc_id；不可见文档不再被检索到。"""
        segment = self.segment_of(doc_id)
        if segment is None:
            return False
        return segment.delete(doc_id)

    def search(
        self,
        query: str | None,
        sort_by: Sequence[SortField] | None = None,
        limit: int = 10,
        doc_filter: DocFilter | None = None,
    ) -> list[SearchHit]:
        """TopK 查询。

        Args:
            query: 查询文本，按分词器规则做 OR 匹配；None 表示匹配全部可见文档。
            sort_by: 多字段排序规范（按顺序生效）；默认 ``_score`` 降序 + ID 升序。
            limit: 返回的最大条数 K（过滤后仍补满，直至候选耗尽）。
            doc_filter: 归并阶段业务过滤器，返回 False 的文档不计入结果。
        """
        if limit < 0:
            raise ValueError("limit 不能为负")
        if limit == 0:
            return []
        fields = tuple(sort_by) if sort_by else (SortField(SCORE_FIELD, descending=True),)

        heap: list[tuple[Any, int, Any]] = []
        streams = [
            segment.iter_ranked(query, fields)
            for segment in self.segments
        ]
        for stream_id, stream in enumerate(streams):
            try:
                key, doc, score = next(stream)
            except StopIteration:
                continue
            heapq.heappush(heap, (key, stream_id, (stream, doc, score)))

        hits: list[SearchHit] = []
        while heap and len(hits) < limit:
            key, stream_id, payload = heapq.heappop(heap)
            stream, doc, score = payload
            if doc_filter is None or doc_filter(doc):
                hits.append(
                    SearchHit(
                        doc_id=doc.doc_id,
                        score=score,
                        document=doc,
                        segment_id=self.segments[stream_id].segment_id,
                    )
                )
            try:
                next_key, next_doc, next_score = next(stream)
            except StopIteration:
                continue
            heapq.heappush(
                heap, (next_key, stream_id, (stream, next_doc, next_score))
            )
        return hits
