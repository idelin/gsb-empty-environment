"""分区 + 排序窗口引擎：ROWS / RANGE 帧上的 SUM 与 RANK（纯标准库实现）。

设计要点：
- 数据按带列类型的批次进入；分区可以横跨任意多个批次，compute() 统一
  计算，因此分块边界永远不会截断分区。
- None 表示 SQL NULL；float("nan") 是普通浮点值，二者严格区分：
  NULL 不参与 SUM，NaN 参与并使结果为 NaN；排序时 NaN 大于一切非 NULL
  数值，NULL 的位置由 nulls_first 显式控制。
- 整数求和直接使用 Python int，任意精度，无溢出问题。
- 重复排序值构成 peer 组：RANK 同组同名次、跳后续名次（1,1,2 -> 1,1,3）；
  RANGE ... CURRENT ROW 的帧端点落在 peer 组边界上，ROWS ... CURRENT ROW
  只含当前行自身。
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Any, Optional, Sequence


class SchemaError(ValueError):
    """批次数据与声明的列类型不一致。"""


class WindowSpecError(ValueError):
    """窗口定义（排序键、帧）非法。"""


_VALUE_CHECKERS = {
    "int": lambda v: isinstance(v, int) and not isinstance(v, bool),
    "float": lambda v: isinstance(v, (int, float)) and not isinstance(v, bool),
    "str": lambda v: isinstance(v, str),
}

_NAN_TOKEN = ("__nan_peer_token__",)


def _group_token(value: Any) -> Any:
    """分区/分组用的规范化键：NaN 与 NaN 同组，None 与 None 同组。"""
    if value is None:
        return None
    if isinstance(value, float) and math.isnan(value):
        return _NAN_TOKEN
    return value


def _is_nan(value: Any) -> bool:
    return isinstance(value, float) and math.isnan(value)


@dataclass(frozen=True)
class OrderKey:
    """排序键。nulls_first 为 None 时按 PostgreSQL 惯例：
    升序 NULLS LAST，降序 NULLS FIRST；显式给出时与方向无关。"""

    column: str
    descending: bool = False
    nulls_first: Optional[bool] = None

    @property
    def effective_nulls_first(self) -> bool:
        return self.nulls_first if self.nulls_first is not None else self.descending


_UNBOUNDED_PRECEDING = "unbounded_preceding"
_PRECEDING = "preceding"
_CURRENT_ROW = "current_row"
_FOLLOWING = "following"
_UNBOUNDED_FOLLOWING = "unbounded_following"

_BOUNDARY_ORDER = {
    _UNBOUNDED_PRECEDING: 0,
    _PRECEDING: 1,
    _CURRENT_ROW: 2,
    _FOLLOWING: 3,
    _UNBOUNDED_FOLLOWING: 4,
}


@dataclass(frozen=True)
class FrameBoundary:
    kind: str
    offset: Optional[int] = None

    def __post_init__(self) -> None:
        if self.kind not in _BOUNDARY_ORDER:
            raise WindowSpecError(f"未知帧边界: {self.kind!r}")
        needs_offset = self.kind in (_PRECEDING, _FOLLOWING)
        if needs_offset and (self.offset is None or self.offset < 0):
            raise WindowSpecError(f"{self.kind} 需要非负 offset")
        if not needs_offset and self.offset is not None:
            raise WindowSpecError(f"{self.kind} 不接受 offset")


UNBOUNDED_PRECEDING = FrameBoundary(_UNBOUNDED_PRECEDING)
UNBOUNDED_FOLLOWING = FrameBoundary(_UNBOUNDED_FOLLOWING)
CURRENT_ROW = FrameBoundary(_CURRENT_ROW)


def PRECEDING(offset: int) -> FrameBoundary:
    return FrameBoundary(_PRECEDING, offset)


def FOLLOWING(offset: int) -> FrameBoundary:
    return FrameBoundary(_FOLLOWING, offset)


@dataclass(frozen=True)
class Frame:
    mode: str  # "rows" | "range"
    start: FrameBoundary
    end: FrameBoundary

    def __post_init__(self) -> None:
        if self.mode not in ("rows", "range"):
            raise WindowSpecError(f"未知帧模式: {self.mode!r}")
        if _BOUNDARY_ORDER[self.start.kind] > _BOUNDARY_ORDER[self.end.kind]:
            raise WindowSpecError("帧起点不能晚于帧终点")

    @property
    def has_offset(self) -> bool:
        return self.start.offset is not None or self.end.offset is not None


class WindowEngine:
    """按批次接收行，compute() 时输出每行的 (sum, rank)。

    schema: [(列名, 类型)]，类型为 "int" / "float" / "str"。
    """

    def __init__(
        self,
        schema: Sequence[tuple[str, str]],
        partition_by: Sequence[str],
        order_by: Sequence[OrderKey],
        sum_column: str,
        frame: Frame,
    ) -> None:
        self._schema = list(schema)
        names = [name for name, _ in self._schema]
        if len(set(names)) != len(names):
            raise SchemaError("列名重复")
        for _, typ in self._schema:
            if typ not in _VALUE_CHECKERS:
                raise SchemaError(f"不支持的列类型: {typ!r}")
        self._col_index = {name: i for i, name in enumerate(names)}

        for col in partition_by:
            if col not in self._col_index:
                raise WindowSpecError(f"分区列不存在: {col!r}")
        if not order_by:
            raise WindowSpecError("RANK 需要至少一个排序键")
        for key in order_by:
            if key.column not in self._col_index:
                raise WindowSpecError(f"排序列不存在: {key.column!r}")
        if sum_column not in self._col_index:
            raise WindowSpecError(f"求和列不存在: {sum_column!r}")
        sum_type = self._schema[self._col_index[sum_column]][1]
        if sum_type not in ("int", "float"):
            raise WindowSpecError("求和列必须是 int 或 float")

        if frame.mode == "range" and frame.has_offset:
            if len(order_by) != 1:
                raise WindowSpecError("RANGE 偏移帧要求单一排序键")
            key = order_by[0]
            if key.descending:
                raise WindowSpecError("RANGE 偏移帧仅支持升序排序键")
            key_type = self._schema[self._col_index[key.column]][1]
            if key_type not in ("int", "float"):
                raise WindowSpecError("RANGE 偏移帧的排序键必须是数值列")

        self._partition_by = list(partition_by)
        self._order_by = list(order_by)
        self._sum_column = sum_column
        self._frame = frame
        self._rows: list[tuple] = []

    def add_batch(self, rows: Sequence[Sequence[Any]]) -> None:
        """追加一个批次；分区可跨批次，不会在批次边界被截断。"""
        width = len(self._schema)
        for row in rows:
            if len(row) != width:
                raise SchemaError(
                    f"行宽度 {len(row)} 与 schema 宽度 {width} 不一致: {row!r}"
                )
            for (name, typ), value in zip(self._schema, row):
                if value is None:
                    continue  # NULL 对任何类型都合法
                if not _VALUE_CHECKERS[typ](value):
                    raise SchemaError(
                        f"列 {name!r} 声明为 {typ}，收到 {type(value).__name__}: {value!r}"
                    )
            self._rows.append(tuple(row))

    def compute(self) -> list[dict]:
        """按输入顺序返回 [{'row':..., 'sum':..., 'rank':...}, ...]。"""
        results: list = [None] * len(self._rows)
        partitions: dict[Any, list[int]] = {}
        for idx, row in enumerate(self._rows):
            pkey = tuple(
                _group_token(row[self._col_index[c]]) for c in self._partition_by
            )
            partitions.setdefault(pkey, []).append(idx)
        for members in partitions.values():
            self._compute_partition(members, results)
        return [
            {"row": self._rows[i], "sum": s, "rank": r}
            for i, (s, r) in enumerate(results)
        ]

    # ---- 内部实现 ----

    def _compare_rows(self, a: tuple, b: tuple) -> int:
        for spec in self._order_by:
            i = self._col_index[spec.column]
            va, vb = a[i], b[i]
            if va is None or vb is None:
                if va is None and vb is None:
                    continue  # NULL 与 NULL 互为 peer
                a_null = va is None
                return -1 if a_null == spec.effective_nulls_first else 1
            if _is_nan(va) or _is_nan(vb):
                if _is_nan(va) and _is_nan(vb):
                    continue  # NaN 与 NaN 互为 peer
                c = 1 if _is_nan(va) else -1  # NaN 大于一切非 NULL 值
            else:
                c = 0 if va == vb else (-1 if va < vb else 1)
            if spec.descending:
                c = -c
            if c:
                return c
        return 0

    def _compute_partition(self, members: list[int], results: list) -> None:
        order = sorted(
            members,
            key=lambda x: _RowSortKey(x, self),
        )
        n = len(order)
        peers: list[tuple[int, int]] = []  # (start, end) 闭区间
        start = 0
        for i in range(1, n + 1):
            if i == n or self._compare_rows(
                self._rows[order[i]], self._rows[order[i - 1]]
            ) != 0:
                peers.append((start, i - 1))
                start = i
        peer_of = [0] * n
        for g, (s, e) in enumerate(peers):
            for i in range(s, e + 1):
                peer_of[i] = g

        sum_idx = self._col_index[self._sum_column]
        for pos in range(n):
            lo, hi = self._frame_bounds(order, pos, peers, peer_of)
            total: Any = None
            for j in range(lo, hi + 1):
                value = self._rows[order[j]][sum_idx]
                if value is None:
                    continue  # NULL 不参与求和
                total = value if total is None else total + value
            rank = peers[peer_of[pos]][0] + 1
            results[order[pos]] = (total, rank)

    def _frame_bounds(self, order, pos, peers, peer_of) -> tuple[int, int]:
        n = len(order)
        frame = self._frame
        if frame.mode == "rows":
            lo = self._rows_boundary(frame.start, pos, n)
            hi = self._rows_boundary(frame.end, pos, n)
        else:
            lo = self._range_boundary(frame.start, order, pos, peers, peer_of, True)
            hi = self._range_boundary(frame.end, order, pos, peers, peer_of, False)
        return max(lo, 0), min(hi, n - 1)  # lo > hi 时为空帧

    @staticmethod
    def _rows_boundary(boundary: FrameBoundary, pos: int, n: int) -> int:
        kind = boundary.kind
        if kind == _UNBOUNDED_PRECEDING:
            return 0
        if kind == _UNBOUNDED_FOLLOWING:
            return n - 1
        if kind == _CURRENT_ROW:
            return pos
        if kind == _PRECEDING:
            return pos - boundary.offset
        return pos + boundary.offset  # _FOLLOWING

    def _range_boundary(self, boundary, order, pos, peers, peer_of, is_start) -> int:
        kind = boundary.kind
        if kind == _UNBOUNDED_PRECEDING:
            return 0
        if kind == _UNBOUNDED_FOLLOWING:
            return len(order) - 1
        if kind == _CURRENT_ROW:
            group = peers[peer_of[pos]]
            return group[0] if is_start else group[1]
        # 带偏移的 RANGE：按排序键取值比较（初始化时已校验为单一升序数值键）
        key_idx = self._col_index[self._order_by[0].column]
        value = self._rows[order[pos]][key_idx]
        if value is None or _is_nan(value):
            # NULL/NaN 行在偏移帧中只与自己的 peer 同帧
            group = peers[peer_of[pos]]
            return group[0] if is_start else group[1]
        delta = boundary.offset if kind == _FOLLOWING else -boundary.offset
        target = value + delta
        keys = [self._rows[idx][key_idx] for idx in order]
        if is_start:
            for j, k in enumerate(keys):
                if k is None or _is_nan(k):
                    continue
                if k >= target:
                    return j
            return len(order)
        for j in range(len(keys) - 1, -1, -1):
            k = keys[j]
            if k is None or _is_nan(k):
                continue
            if k <= target:
                return j
        return -1


class _RowSortKey:
    """sorted() 用的包装键，把行索引比较委托给引擎的比较器。"""

    __slots__ = ("idx", "engine")

    def __init__(self, idx: int, engine: "WindowEngine") -> None:
        self.idx = idx
        self.engine = engine

    def __lt__(self, other: "_RowSortKey") -> bool:
        return (
            self.engine._compare_rows(
                self.engine._rows[self.idx], self.engine._rows[other.idx]
            )
            < 0
        )
