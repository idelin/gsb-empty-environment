"""整数区间属性的相交 / 包含 / 被包含索引。

索引结构（仅用标准库 ``bisect`` 维护有序列表）
------------------------------------------------
每篇当前文档在每个列表中至多出现一次，因此任何查询都不会产生重复 id：

* ``lo_rows``：按 (lo, hi, doc_id) 排序，按左端点裁剪候选；
* ``hi_rows``：按 (hi, lo, doc_id) 排序，按右端点裁剪候选；
* 无界端用哨兵整数表示（左无界 lo=-INF，右无界 hi=INF），
  因此三种关系都可以转化为有序列表上的二分查找；
* 属性缺失的文档只进 ``missing`` 集合，空区间只进 ``empty`` 集合，
  二者不进入区间索引，也永远不匹配相交/包含/被包含查询。

upsert 语义
-----------
更新文档时先用旧区间从两个有序列表中摘除旧索引项（以及
missing/empty 标记），再按新区间插入，保证旧索引项被删除。
"""

from __future__ import annotations

import bisect
import os

from .document import Document, read_document
from .interval import INF, Interval

INF_SENTINEL = INF + 1

INTERSECTS = "intersects"
CONTAINS = "contains"        # 查询区间包含文档区间
CONTAINED_BY = "contained_by"  # 查询区间被文档区间包含
MISSING = "missing"

_RELATIONS = (INTERSECTS, CONTAINS, CONTAINED_BY)


class Catalog:
    """持有文档与区间索引的内存目录。"""

    def __init__(self) -> None:
        self._docs: dict[str, Document] = {}
        self._lo_rows: list[tuple[int, int, str]] = []
        self._hi_rows: list[tuple[int, int, str]] = []
        self._missing: set[str] = set()
        self._empty: set[str] = set()

    # ------------------------------------------------------------------ 装载

    @classmethod
    def from_directory(cls, dirpath: str, *, suffix: str = ".txt") -> "Catalog":
        catalog = cls()
        for name in sorted(os.listdir(dirpath)):
            if not name.endswith(suffix):
                continue
            catalog.upsert_document(read_document(os.path.join(dirpath, name)))
        return catalog

    # ------------------------------------------------------------------ 变更

    def upsert_document(self, doc: Document) -> None:
        """新增文档；同 id 已存在时先删除旧索引项再按新值插入。"""
        old = self._docs.get(doc.doc_id)
        if old is not None:
            self._detach(doc.doc_id, old.interval)
        self._docs[doc.doc_id] = doc
        self._attach(doc)

    def remove_document(self, doc_id: str) -> bool:
        doc = self._docs.pop(doc_id, None)
        if doc is None:
            return False
        self._detach(doc.doc_id, doc.interval)
        return True

    def _attach(self, doc: Document) -> None:
        iv = doc.interval
        if iv.is_missing:
            self._missing.add(doc.doc_id)
            return
        if iv.is_empty:
            self._empty.add(doc.doc_id)
            return
        bisect.insort(self._lo_rows, (iv.lo, iv.hi, doc.doc_id))
        bisect.insort(self._hi_rows, (iv.hi, iv.lo, doc.doc_id))

    def _detach(self, doc_id: str, iv: Interval) -> None:
        if iv.is_missing:
            self._missing.discard(doc_id)
            return
        if iv.is_empty:
            self._empty.discard(doc_id)
            return
        self._delete_row(self._lo_rows, iv.lo, iv.hi, doc_id)
        self._delete_row(self._hi_rows, iv.hi, iv.lo, doc_id)

    @staticmethod
    def _delete_row(rows: list[tuple[int, int, str]], first: int,
                    second: int, doc_id: str) -> None:
        # 用三元组哨兵把搜索范围限定在首元素等于 first 的行段内。
        start = bisect.bisect_left(rows, (first, -INF_SENTINEL, ""))
        end = bisect.bisect_right(
            rows, (first, INF_SENTINEL, "􏿿"))
        for pos in range(start, end):
            if rows[pos][1] == second and rows[pos][2] == doc_id:
                del rows[pos]
                return
        raise LookupError(f"索引项已损坏，未找到文档 {doc_id!r} 的行")

    # ------------------------------------------------------------------ 查询

    def get(self, doc_id: str) -> Document | None:
        return self._docs.get(doc_id)

    def missing_ids(self) -> list[str]:
        return sorted(self._missing)

    def empty_ids(self) -> list[str]:
        return sorted(self._empty)

    def query(self, query: "Interval | str", mode: str = INTERSECTS) -> list[str]:
        """走索引返回匹配文档 id（按 id 排序、无重复）。"""
        if mode == MISSING:
            return self.missing_ids()
        if mode not in _RELATIONS:
            raise ValueError(f"未知查询模式: {mode!r}")
        q = query if isinstance(query, Interval) else Interval.parse(query)
        if q.is_missing or q.is_empty:
            return []
        if mode == INTERSECTS:
            hits = self._query_intersects(q)
        elif mode == CONTAINS:
            hits = self._query_contains(q)
        else:
            hits = self._query_contained_by(q)
        return sorted(hits)

    def _query_intersects(self, q: Interval) -> set[str]:
        # lo <= q.hi 且 hi >= q.lo；同一文档在 lo_rows 中只出现一次。
        stop = bisect.bisect_right(self._lo_rows, (q.hi + 1,))
        return {doc_id for _, hi, doc_id in self._lo_rows[:stop] if hi >= q.lo}

    def _query_contains(self, q: Interval) -> set[str]:
        # 文档区间被查询区间包含：doc.lo >= q.lo 且 doc.hi <= q.hi。
        start = bisect.bisect_left(self._lo_rows, (q.lo,))
        return {doc_id for _, hi, doc_id in self._lo_rows[start:]
                if hi <= q.hi}

    def _query_contained_by(self, q: Interval) -> set[str]:
        # 文档区间包含查询区间：doc.lo <= q.lo 且 doc.hi >= q.hi。
        stop = bisect.bisect_right(self._lo_rows, (q.lo + 1,))
        return {doc_id for _, hi, doc_id in self._lo_rows[:stop] if hi >= q.hi}

    def full_scan(self, query: "Interval | str | None",
                  mode: str = INTERSECTS) -> list[str]:
        """线性扫描全部文档，作为索引查询的正确性基准。"""
        if mode == MISSING:
            return sorted(doc_id for doc_id, doc in self._docs.items()
                          if doc.interval.is_missing)
        if mode not in _RELATIONS:
            raise ValueError(f"未知查询模式: {mode!r}")
        q = query if isinstance(query, Interval) else Interval.parse(query)
        if q.is_missing or q.is_empty:
            return []
        hits: list[str] = []
        for doc_id, doc in self._docs.items():
            iv = doc.interval
            if iv.is_missing or iv.is_empty:
                continue
            if mode == INTERSECTS:
                matched = iv.lo <= q.hi and q.lo <= iv.hi
            elif mode == CONTAINS:
                matched = q.lo <= iv.lo and iv.hi <= q.hi
            else:
                matched = iv.lo <= q.lo and q.hi <= iv.hi
            if matched:
                hits.append(doc_id)
        return sorted(hits)

    # ------------------------------------------------------------------ 观测

    def __len__(self) -> int:
        return len(self._docs)

    def __contains__(self, doc_id: object) -> bool:
        return doc_id in self._docs

    def index_stats(self) -> dict[str, int]:
        return {
            "documents": len(self._docs),
            "lo_rows": len(self._lo_rows),
            "hi_rows": len(self._hi_rows),
            "missing": len(self._missing),
            "empty": len(self._empty),
        }

    def lo_rows(self) -> tuple[tuple[int, int, str], ...]:
        return tuple(self._lo_rows)

    def hi_rows(self) -> tuple[tuple[int, int, str], ...]:
        return tuple(self._hi_rows)
