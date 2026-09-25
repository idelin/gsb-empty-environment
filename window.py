"""分区分块窗口计算引擎（仅标准库，Python 3.11+）。

能力：
- 输入为带列类型的批次（Batch），NULL 用 None 表示，NaN 与 NULL 不同；
- PARTITION BY + ORDER BY，NULL 位置由 nulls_first 显式指定（与升/降序无关），
  NaN 视为"最大普通值"：升序排最后、降序排最前；
- RANK()：重复排序值构成 peer 组，组内并列、组后跳号（键 1,1,2 -> 1,1,3）；
- SUM()：支持 ROWS / RANGE 窗口帧；ROWS 的 CURRENT ROW 只含自身，
  RANGE 的 CURRENT ROW 含整个 peer 组；SUM 忽略 NULL，全 NULL 得 NULL；
- 分块输入：同一分区的行须连续到达，新分区键出现时结算上一分区，
  因此分块边界不会截断分区；
- 整数精确计算（Python int 任意精度），浮点出现时才退化为 float。
"""

from __future__ import annotations

import math
from bisect import bisect_left, bisect_right
from dataclasses import dataclass
from functools import cmp_to_key
from typing import Optional, Sequence


# ---------------------------------------------------------------- 批次输入

_TYPE_CHECKS = {
    "int": lambda v: isinstance(v, int) and not isinstance(v, bool),
    "float": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
    "str": lambda v: isinstance(v, str),
}


@dataclass(frozen=True)
class Column:
    name: str
    type: str  # "int" | "float" | "str"


class Batch:
    """一个输入分块：列类型声明 + 行（dict 列表）。"""

    def __init__(self, columns: Sequence[Column], rows: Sequence[dict]):
        self.columns = list(columns)
        self.rows = [dict(r) for r in rows]
        by_name = {c.name: c for c in self.columns}
        for i, row in enumerate(self.rows):
            for name, value in row.items():
                col = by_name.get(name)
                if col is None:
                    raise ValueError(f"第 {i} 行出现未知列 {name!r}")
                if value is None:
                    continue  # NULL 对任何列类型都合法
                if col.type not in _TYPE_CHECKS:
                    raise ValueError(f"未知列类型 {col.type!r}")
                if not _TYPE_CHECKS[col.type](value):
                    raise TypeError(
                        f"第 {i} 行 {name!r} 列应为 {col.type}，得到 {value!r}"
                    )


# ---------------------------------------------------------------- 排序

_NULL, _VALUE, _NAN = 0, 1, 2
_NAN_TOKEN = object()  # 分区键中 NaN 的归一化占位（NaN != NaN）


def _bucket(v):
    if v is None:
        return _NULL
    if isinstance(v, float) and math.isnan(v):
        return _NAN
    return _VALUE


@dataclass(frozen=True)
class OrderKey:
    column: str
    desc: bool = False
    nulls_first: bool = False  # 显式 NULL 位置，与 desc 无关


def _cmp_scalar(a, b):
    return (a > b) - (a < b)


def make_row_comparator(order_by: Sequence[OrderKey]):
    def cmp(ra, rb):
        for key in order_by:
            a, b = ra[key.column], rb[key.column]
            ba, bb = _bucket(a), _bucket(b)
            if ba == bb:
                if ba == _VALUE:
                    r = _cmp_scalar(a, b)
                    if r:
                        return -r if key.desc else r
                continue  # NULL==NULL，NaN==NaN
            if _NULL in (ba, bb):
                a_first = (ba == _NULL) == key.nulls_first
                return -1 if a_first else 1
            r = 1 if ba == _NAN else -1  # NaN 大于一切普通值
            return -r if key.desc else r
        return 0

    return cmp


def make_peer_eq(order_by: Sequence[OrderKey]):
    def eq(ra, rb):
        for key in order_by:
            a, b = ra[key.column], rb[key.column]
            if _bucket(a) != _bucket(b):
                return False
            if _bucket(a) == _VALUE and a != b:
                return False
        return True

    return eq


# ---------------------------------------------------------------- 窗口帧


@dataclass(frozen=True)
class Bound:
    kind: str  # unbounded_preceding | preceding | current_row | following | unbounded_following
    offset: Optional[int] = None


UNBOUNDED_PRECEDING = Bound("unbounded_preceding")
CURRENT_ROW = Bound("current_row")
UNBOUNDED_FOLLOWING = Bound("unbounded_following")


def PRECEDING(n: int) -> Bound:
    return Bound("preceding", n)


def FOLLOWING(n: int) -> Bound:
    return Bound("following", n)


@dataclass(frozen=True)
class Frame:
    mode: str  # "rows" | "range"
    start: Bound = UNBOUNDED_PRECEDING
    end: Bound = CURRENT_ROW


# ---------------------------------------------------------------- 度量


@dataclass(frozen=True)
class Measure:
    kind: str  # "rank" | "sum"
    output: str
    column: Optional[str] = None
    frame: Optional[Frame] = None


def RANK(output: str = "rank") -> Measure:
    return Measure("rank", output)


def SUM(column: str, frame: Frame, output: Optional[str] = None) -> Measure:
    return Measure("sum", output or f"sum_{column}", column, frame)


# ---------------------------------------------------------------- 分区计算


def _resolve_rows_bound(bound: Bound, i: int, n: int) -> int:
    if bound.kind == "unbounded_preceding":
        return 0
    if bound.kind == "unbounded_following":
        return n - 1
    if bound.kind == "current_row":
        return i
    if bound.kind == "preceding":
        return i - bound.offset
    if bound.kind == "following":
        return i + bound.offset
    raise ValueError(f"未知边界 {bound.kind!r}")


def compute_partition(rows: list, order_by, measures) -> list:
    """对单个分区（未排序的原始行）计算全部度量，按输入顺序返回。"""
    n = len(rows)
    cmp = make_row_comparator(order_by)
    order = sorted(range(n), key=cmp_to_key(lambda i, j: cmp(rows[i], rows[j])))
    srows = [rows[i] for i in order]

    eq = make_peer_eq(order_by)
    starts, ends = [], []  # 每个 peer 组的 [start, end)
    pos = 0
    while pos < n:
        j = pos + 1
        while j < n and eq(srows[pos], srows[j]):
            j += 1
        starts.append(pos)
        ends.append(j)
        pos = j
    group_of = [0] * n
    for g, (s, e) in enumerate(zip(starts, ends)):
        for p in range(s, e):
            group_of[p] = g

    # RANGE 偏移帧所需的数值键段（升序连续段，NULL/NaN 不参与）
    _num_seg = None

    def numeric_segment():
        nonlocal _num_seg
        if _num_seg is None:
            if len(order_by) != 1 or order_by[0].desc:
                raise ValueError("RANGE 偏移帧要求单一升序数值排序键")
            poss, keys = [], []
            col = order_by[0].column
            for p, r in enumerate(srows):
                v = r[col]
                if _bucket(v) == _VALUE and isinstance(v, (int, float)):
                    poss.append(p)
                    keys.append(v)
            _num_seg = (poss, keys)
        return _num_seg

    def range_start(bound: Bound, p: int) -> int:
        g = group_of[p]
        if bound.kind == "unbounded_preceding":
            return 0
        if bound.kind == "current_row":
            return starts[g]
        key = srows[p][order_by[0].column]
        if _bucket(key) != _VALUE or not isinstance(key, (int, float)):
            return starts[g]  # NULL/NaN 的偏移帧只覆盖自身 peer 组
        if bound.kind == "preceding":
            target = key - bound.offset
        elif bound.kind == "following":
            target = key + bound.offset
        else:
            raise ValueError(f"RANGE 起始边界不支持 {bound.kind!r}")
        poss, keys = numeric_segment()
        idx = bisect_left(keys, target)
        if idx == len(poss):
            return poss[-1] + 1 if poss else 0  # 空帧
        return poss[idx]

    def range_end(bound: Bound, p: int) -> int:
        g = group_of[p]
        if bound.kind == "unbounded_following":
            return n - 1
        if bound.kind == "current_row":
            return ends[g] - 1
        key = srows[p][order_by[0].column]
        if _bucket(key) != _VALUE or not isinstance(key, (int, float)):
            return ends[g] - 1
        if bound.kind == "preceding":
            target = key - bound.offset
        elif bound.kind == "following":
            target = key + bound.offset
        else:
            raise ValueError(f"RANGE 结束边界不支持 {bound.kind!r}")
        poss, keys = numeric_segment()
        idx = bisect_right(keys, target)
        if idx == 0:
            return -1  # 空帧
        return poss[idx - 1]

    out_sorted = [dict() for _ in range(n)]
    for m in measures:
        if m.kind == "rank":
            for p in range(n):
                out_sorted[p][m.output] = starts[group_of[p]] + 1
        elif m.kind == "sum":
            vals = [r[m.column] for r in srows]
            pref = [0] * (n + 1)  # 非 NULL 值的前缀和（int 保持精确）
            cnt = [0] * (n + 1)  # 非 NULL 值的前缀计数
            for p, v in enumerate(vals):
                pref[p + 1] = pref[p] + (v if v is not None else 0)
                cnt[p + 1] = cnt[p] + (v is not None)
            for p in range(n):
                frame = m.frame
                if frame.mode == "rows":
                    lo = _resolve_rows_bound(frame.start, p, n)
                    hi = _resolve_rows_bound(frame.end, p, n)
                elif frame.mode == "range":
                    lo = range_start(frame.start, p)
                    hi = range_end(frame.end, p)
                else:
                    raise ValueError(f"未知帧模式 {frame.mode!r}")
                lo = max(lo, 0)
                hi = min(hi, n - 1)
                if lo > hi or cnt[hi + 1] - cnt[lo] == 0:
                    out_sorted[p][m.output] = None  # 空帧或全 NULL
                else:
                    out_sorted[p][m.output] = pref[hi + 1] - pref[lo]
        else:
            raise ValueError(f"未知度量 {m.kind!r}")

    results = [None] * n
    for p in range(n):
        row = dict(rows[order[p]])
        row.update(out_sorted[p])
        results[order[p]] = row
    return results


# ---------------------------------------------------------------- 流式引擎


class WindowEngine:
    """分块流式引擎。

    假设同一分区的行在输入中连续到达（跨分块连续亦可）。
    新分区键出现时结算并输出上一分区，因此分块边界不会截断分区。
    """

    def __init__(self, partition_by, order_by, measures):
        self.partition_by = list(partition_by)
        self.order_by = list(order_by)
        self.measures = list(measures)
        self._pending: list = []
        self._pending_key = None

    def _pkey(self, row):
        return tuple(
            _NAN_TOKEN if _bucket(row[c]) == _NAN else row[c]
            for c in self.partition_by
        )

    def add_batch(self, batch: Batch) -> list:
        out = []
        for row in batch.rows:
            k = self._pkey(row)
            if self._pending and k != self._pending_key:
                out.extend(self._flush())
            if not self._pending:
                self._pending_key = k
            self._pending.append(row)
        return out

    def finish(self) -> list:
        if not self._pending:
            return []
        return self._flush()

    def _flush(self) -> list:
        rows, self._pending = self._pending, []
        self._pending_key = None
        return compute_partition(rows, self.order_by, self.measures)


def run(batches, partition_by, order_by, measures) -> list:
    """便捷入口：依次喂入所有分块并返回全部输出行。"""
    eng = WindowEngine(partition_by, order_by, measures)
    out = []
    for b in batches:
        out.extend(eng.add_batch(b))
    out.extend(eng.finish())
    return out
