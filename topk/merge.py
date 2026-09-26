"""K 路归并 TopK：流式归并 + 过滤后补足候选。

关键点：过滤判定发生在归并弹出阶段。某段队首候选被过滤（或段头
整段被过滤）时，立即从该段继续读取下一条补足，直到集满 K 条
可见且通过过滤的文档，或所有段耗尽。绝不"先取各段前 K 条原始项
再过滤"——那样在过滤命中率高时会漏掉本应入选的文档。
"""

from __future__ import annotations

import heapq
from typing import Callable, Iterator, Optional

from .document import Document
from .filters import Filter
from .segment import Segment
from .sortspec import SortField, make_comparator


class _HeapItem:
    """堆元素：按比较器定义序，比较器含 doc_id 兜底，构成全序。"""

    __slots__ = ("doc", "cursor_index", "_compare")

    def __init__(self, doc: Document, cursor_index: int, compare):
        self.doc = doc
        self.cursor_index = cursor_index
        self._compare = compare

    def __lt__(self, other: "_HeapItem") -> bool:
        return self._compare(self.doc, other.doc) < 0


def merge_topk(
    segments: list[Segment],
    sort_fields: list[SortField],
    k: int,
    filter: Optional[Filter] = None,
) -> list[Document]:
    """从多个段归并出 TopK。

    segments:    索引段列表
    sort_fields: 多字段排序规格（升降序 + null 位置），同分按 doc_id 升序
    k:           返回条数；不足 K 条时返回全部符合条件的文档
    filter:      可选过滤条件；被过滤的候选不占名额，自动向后补足
    """
    if k <= 0:
        return []

    compare = make_comparator(sort_fields)
    cursors: list[Iterator[Document]] = [seg.cursor(compare) for seg in segments]

    heap: list[_HeapItem] = []

    def _push_next(cursor_index: int) -> None:
        doc = next(cursors[cursor_index], None)
        if doc is not None:
            heapq.heappush(heap, _HeapItem(doc, cursor_index, compare))

    for index in range(len(cursors)):
        _push_next(index)

    results: list[Document] = []
    while heap and len(results) < k:
        item = heapq.heappop(heap)
        if filter is None or filter(item.doc):
            results.append(item.doc)
        # 无论是否通过过滤，都从该段补足下一条候选
        _push_next(item.cursor_index)

    return results
