"""整数区间属性索引：相交 / 包含 / 被包含查询。

设计约定
========
1. 区间一律先**规范化**为闭区间 [lo, hi]（lo/hi 为 int 或 None 表示 -inf/+inf）：
   - 开下界 (v,   ->  lo = v + 1
   - 开上界 , v)  ->  hi = v - 1
   - 缺失端点     ->  None（负/正无穷）
   规范化后若 lo > hi，则区间为空（如 (3,3)、(2,3)）。
2. 空区间约定：文档属性中出现空区间时视为**缺失属性**（不入索引）；
   查询区间为空时构造 Interval 会抛出 ValueError。
3. 点区间 [v, v] 规范化为 lo == hi == v，是合法区间，恰好覆盖整数 v。
   因此 [0,5) 与 [5,8) 不相交（前者 hi=4，后者 lo=5），
   而 [0,5] 与 [5,8) 相交于点 5。
4. 三种查询语义（Q 为查询区间，D 为文档区间，均已规范化）：
   - 相交   intersect(Q):    D.lo <= Q.hi 且 D.hi >= Q.lo
   - 包含   contains(Q):     D ⊇ Q，即 D.lo <= Q.lo 且 D.hi >= Q.hi
   - 被包含 contained_by(Q): D ⊆ Q，即 D.lo >= Q.lo 且 D.hi <= Q.hi
5. 文档更新（upsert 同一 doc_id）时先删除旧索引项再插入新项；
   delete 删除全部索引项。缺失区间属性的文档不进入区间索引。
6. 查询结果内部用 set 去重，返回排序后的列表，保证无重复 ID，
   并与 full_scan_* 全扫描结果完全一致。

分词规则（文档为本地合成 UTF-8 文本）
====================================
1. 文本按 UTF-8 解码。
2. 先用正则 RANGE_RE 提取区间属性，形如 range=[0,5)、range=(,8]、range=[3,)；
   端点可省略表示缺失。若出现多个 range= 规范，取第一个，其余忽略。
3. 去掉区间规范后的剩余文本，用 Unicode 正则 \\w+ 切分出词元
   （字母/数字/下划线的极大连续串），并做 casefold 小写化。
4. 词元存入一个简单的倒排表（doc_id 集合），仅用于演示，不依赖任何
   搜索服务或全文检索库。
"""

from __future__ import annotations

import bisect
import re
from dataclasses import dataclass
from typing import Hashable, Optional

_INF = float("inf")

RANGE_RE = re.compile(
    r"range\s*=\s*([\[\(])\s*(-?\d+)?\s*,\s*(-?\d+)?\s*([\]\)])"
)
TOKEN_RE = re.compile(r"\w+", re.UNICODE)


@dataclass(frozen=True)
class Interval:
    """规范化后的闭整数区间；lo/hi 为 None 表示负/正无穷。"""

    lo: Optional[int]
    hi: Optional[int]

    def __post_init__(self) -> None:
        if self.lo is not None and self.hi is not None and self.lo > self.hi:
            raise ValueError(f"空区间: [{self.lo}, {self.hi}]")

    @staticmethod
    def from_spec(
        lo: Optional[int],
        lo_closed: bool,
        hi: Optional[int],
        hi_closed: bool,
    ) -> Optional["Interval"]:
        """由开闭端点规格构造规范化区间；空区间返回 None。"""
        nlo = lo if (lo is None or lo_closed) else lo + 1
        nhi = hi if (hi is None or hi_closed) else hi - 1
        if nlo is not None and nhi is not None and nlo > nhi:
            return None
        return Interval(nlo, nhi)

    def intersects(self, other: "Interval") -> bool:
        if self.hi is not None and other.lo is not None and self.hi < other.lo:
            return False
        if self.lo is not None and other.hi is not None and self.lo > other.hi:
            return False
        return True

    def contains(self, other: "Interval") -> bool:
        # 下界：self.lo <= other.lo（None 视为 -inf）
        if other.lo is None:
            if self.lo is not None:
                return False
        elif self.lo is not None and self.lo > other.lo:
            return False
        # 上界：self.hi >= other.hi（None 视为 +inf）
        if other.hi is None:
            if self.hi is not None:
                return False
        elif self.hi is not None and self.hi < other.hi:
            return False
        return True

    def contained_by(self, other: "Interval") -> bool:
        return other.contains(self)


def parse_interval(text: str) -> Optional[Interval]:
    """从文本中解析第一个 range= 区间规范。

    返回 None 表示属性缺失，或规范化为空区间（按约定视为缺失）。
    """
    m = RANGE_RE.search(text)
    if not m:
        return None
    lo_closed = m.group(1) == "["
    hi_closed = m.group(4) == "]"
    lo = int(m.group(2)) if m.group(2) is not None else None
    hi = int(m.group(3)) if m.group(3) is not None else None
    return Interval.from_spec(lo, lo_closed, hi, hi_closed)


def tokenize(text: str) -> list[str]:
    """按分词规则切词：先剔除 range= 规范，再 \\w+ 切分并 casefold。"""
    body = RANGE_RE.sub(" ", text, count=1)
    return [tok.casefold() for tok in TOKEN_RE.findall(body)]


def _lo_key(iv: Interval) -> tuple:
    # 升序键：-inf 最小
    return (0, 0) if iv.lo is None else (1, iv.lo)


def _hi_key(iv: Interval) -> tuple:
    # 升序键：+inf 最大
    return (1, 0) if iv.hi is None else (0, iv.hi)


class IntervalIndex:
    """基于两个有序端点列表（bisect 维护）的区间索引。"""

    def __init__(self) -> None:
        self._docs: dict[Hashable, Interval] = {}
        self._tokens: dict[Hashable, list[str]] = {}
        self._token_postings: dict[str, set] = {}
        self._by_lo: list[tuple] = []          # ((lo_key), seq)，升序
        self._by_hi: list[tuple] = []          # ((hi_key), seq)，升序
        self._entry: dict[Hashable, tuple] = {}  # doc_id -> (lo_entry, hi_entry)
        self._id_by_seq: dict[int, Hashable] = {}
        self._seq = 0

    # ---- 写入 ----

    def upsert(self, doc_id: Hashable, text: str) -> None:
        """插入或更新文档；更新时先删除旧索引项。"""
        if doc_id in self._tokens:
            self.delete(doc_id)
        tokens = tokenize(text)
        self._tokens[doc_id] = tokens
        for tok in set(tokens):
            self._token_postings.setdefault(tok, set()).add(doc_id)
        iv = parse_interval(text)
        if iv is None:
            return  # 缺失（或空区间）属性：不进入区间索引
        self._seq += 1
        lo_entry = (_lo_key(iv), self._seq)
        hi_entry = (_hi_key(iv), self._seq)
        bisect.insort(self._by_lo, lo_entry)
        bisect.insort(self._by_hi, hi_entry)
        self._docs[doc_id] = iv
        self._entry[doc_id] = (lo_entry, hi_entry)
        self._id_by_seq[self._seq] = doc_id

    def delete(self, doc_id: Hashable) -> None:
        """删除文档的全部索引项；不存在时静默返回。"""
        tokens = self._tokens.pop(doc_id, None)
        if tokens is not None:
            for tok in set(tokens):
                posting = self._token_postings.get(tok)
                if posting is not None:
                    posting.discard(doc_id)
                    if not posting:
                        del self._token_postings[tok]
        iv = self._docs.pop(doc_id, None)
        if iv is None:
            return
        lo_entry, hi_entry = self._entry.pop(doc_id)
        i = bisect.bisect_left(self._by_lo, lo_entry)
        assert self._by_lo[i] == lo_entry
        self._by_lo.pop(i)
        j = bisect.bisect_left(self._by_hi, hi_entry)
        assert self._by_hi[j] == hi_entry
        self._by_hi.pop(j)
        del self._id_by_seq[lo_entry[1]]

    # ---- 索引查询 ----

    def _ids(self, entries: list[tuple]) -> set:
        return {self._id_by_seq[seq] for _, seq in entries}

    def _lo_le(self, lo_key: tuple) -> set:
        return self._ids(self._by_lo[: bisect.bisect_right(self._by_lo, (lo_key, _INF))])

    def _lo_ge(self, lo_key: tuple) -> set:
        return self._ids(self._by_lo[bisect.bisect_left(self._by_lo, (lo_key, 0)) :])

    def _hi_ge(self, hi_key: tuple) -> set:
        return self._ids(self._by_hi[bisect.bisect_left(self._by_hi, (hi_key, 0)) :])

    def _hi_le(self, hi_key: tuple) -> set:
        return self._ids(self._by_hi[: bisect.bisect_right(self._by_hi, (hi_key, _INF))])

    def intersect(self, q: Interval) -> list:
        lo_side = self._lo_le((1, q.hi)) if q.hi is not None else set(self._docs)
        hi_side = self._hi_ge((0, q.lo)) if q.lo is not None else set(self._docs)
        return sorted(lo_side & hi_side)

    def contains(self, q: Interval) -> list:
        lo_key = (0, 0) if q.lo is None else (1, q.lo)
        hi_key = (1, 0) if q.hi is None else (0, q.hi)
        return sorted(self._lo_le(lo_key) & self._hi_ge(hi_key))

    def contained_by(self, q: Interval) -> list:
        lo_side = self._lo_ge((1, q.lo)) if q.lo is not None else set(self._docs)
        hi_side = self._hi_le((0, q.hi)) if q.hi is not None else set(self._docs)
        return sorted(lo_side & hi_side)

    # ---- 全扫描参照实现 ----

    def full_scan_intersect(self, q: Interval) -> list:
        return sorted(d for d, iv in self._docs.items() if iv.intersects(q))

    def full_scan_contains(self, q: Interval) -> list:
        return sorted(d for d, iv in self._docs.items() if iv.contains(q))

    def full_scan_contained_by(self, q: Interval) -> list:
        return sorted(d for d, iv in self._docs.items() if iv.contained_by(q))

    # ---- 词元倒排（演示用） ----

    def docs_with_token(self, token: str) -> list:
        return sorted(self._token_postings.get(token.casefold(), set()))

    # ---- 内部一致性（测试用） ----

    def check_invariants(self) -> None:
        assert len(self._by_lo) == len(self._by_hi) == len(self._docs)
        assert self._by_lo == sorted(self._by_lo)
        assert self._by_hi == sorted(self._by_hi)
        assert {s for _, s in self._by_lo} == {s for _, s in self._by_hi} == set(self._id_by_seq)
        for doc, iv in self._docs.items():
            lo_entry, hi_entry = self._entry[doc]
            assert lo_entry[0] == _lo_key(iv) and hi_entry[0] == _hi_key(iv)
            assert self._id_by_seq[lo_entry[1]] == doc
