"""并发占用的两套增量索引。

为什么不用"每次查询重扫全部区间"：

* :class:`EventIndex` 把每个半开区间 ``[s,e)`` 转成两个增量事件
  （``s:+k``、``e:-k``），树节点保存子树净增量 ``total`` 与事件后的最大
  前缀增量 ``interior_pmax``。查询把进入占用、lo 边界事件和内部子树前缀
  组合成峰值；增删一个区间只改两个时间点、
  每条更新路径上的聚合值为 O(1) 重算，整体期望 O(log n)。
* :class:`IntervalIndex` 按 start 排序并维护子树最大 end，支持"某时刻有哪些
  占用处于活跃状态"的剪枝 stabbing 查询，用于把峰值时段回溯到具体占用 ID。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Iterator, Optional, Set, Tuple

from .treap import REMOVE, Treap, TreapNode


# --------------------------------------------------------------------------
# 事件点索引：delta 事件 + 子树 (total, interior_pmax)
# --------------------------------------------------------------------------


@dataclass
class Point:
    """单个时间点上的净增量及贡献者。"""

    delta: int = 0
    starts: Set[str] = None  # type: ignore[assignment]
    ends: Set[str] = None    # type: ignore[assignment]

    def __post_init__(self) -> None:
        if self.starts is None:
            self.starts = set()
        if self.ends is None:
            self.ends = set()


@dataclass(frozen=True)
class PointAgg:
    """事件点子树聚合：元素个数、净增量和、事件后的最大前缀和。"""

    count: int
    total: int
    interior_pmax: int


def _point_singleton(_key: int, point: Point) -> PointAgg:
    return PointAgg(
        count=1,
        total=point.delta,
        interior_pmax=point.delta,
    )


def _point_combine(
    left: Optional[PointAgg], point: Point, right: Optional[PointAgg]
) -> PointAgg:
    empty = PointAgg(0, 0, -10**30)
    left = left or empty
    right = right or empty
    total = left.total + point.delta + right.total
    # interior_pmax 只统计严格落在前缀中事件之后的占用，不含"尚未发生任何事"
    # 的 0 占用，用于区分"跨整段活跃"与"内部无事件即无人"。
    interior_pmax = max(
        (left.interior_pmax if left.count else -10**30),
        left.total + point.delta,
        (
            left.total + point.delta + max(0, right.interior_pmax)
            if right.count else -10**30
        ),
    )
    return PointAgg(left.count + 1 + right.count, total, interior_pmax)


class EventIndex:
    def __init__(self) -> None:
        self._tree = Treap(_point_singleton, _point_combine)

    def apply_delta(self, t: int, amount: int, occupancy_id: str, is_start: bool) -> None:
        """在时间点 ``t`` 叠加 +amount/-amount；点被抵消为空时删除该键。"""

        def make() -> Point:
            point = Point()
            _apply(point)
            return point

        def mutate(point: Point):
            _apply(point)
            if point.delta == 0 and not point.starts and not point.ends:
                return REMOVE
            return point

        def _apply(point: Point) -> None:
            point.delta += amount
            if is_start:
                (point.starts.add if amount > 0 else point.starts.discard)(occupancy_id)
            else:
                (point.ends.add if amount < 0 else point.ends.discard)(occupancy_id)

        self._tree.upsert(t, make, mutate)

    def add_interval(self, start: int, end: int, occupancy_id: str) -> None:
        if start == end:
            return
        self.apply_delta(start, +1, occupancy_id, is_start=True)
        self.apply_delta(end, -1, occupancy_id, is_start=False)

    def remove_interval(self, start: int, end: int, occupancy_id: str) -> None:
        if start == end:
            return
        self.apply_delta(start, -1, occupancy_id, is_start=True)
        self.apply_delta(end, +1, occupancy_id, is_start=False)

    def range_prefix_total(self, t: int) -> int:
        """严格早于 ``t`` 的所有事件净增量之和，即进入 ``[t, ...)`` 时的占用。"""
        with self._tree.split_range(None, t) as left:
            return left.agg.total if left is not None else 0

    def point_total(self, t: int) -> int:
        """时间点 ``t`` 自身的净增量（同点先关后开后的合计）。"""
        point = self._tree.get(t)
        return point.delta if point is not None else 0

    def range_agg(self, lo: int, hi: Optional[int]) -> Optional[PointAgg]:
        with self._tree.split_range(lo, hi) as mid:
            return None if mid is None else mid.agg

    def range_nodes(self, lo: int, hi: Optional[int]) -> Iterator[TreapNode]:
        with self._tree.split_range(lo, hi) as mid:
            yield from Treap.walk_subtree(mid)

    def points_in_order(self) -> Iterator[Tuple[int, Point]]:
        yield from self._tree.in_order()


# --------------------------------------------------------------------------
# 活跃区间索引：按 start 排序 + 子树最大 end，用于证据反查
# --------------------------------------------------------------------------


@dataclass(frozen=True)
class IntervalValue:
    end: int
    occupancy_id: str


@dataclass(frozen=True)
class IntervalAgg:
    count: int
    max_end: int


def _interval_singleton(key: Tuple[int, str], value: IntervalValue) -> IntervalAgg:
    return IntervalAgg(1, value.end)


def _interval_combine(
    left: Optional[IntervalAgg],
    value: IntervalValue,
    right: Optional[IntervalAgg],
) -> IntervalAgg:
    max_end = value.end
    if left is not None:
        max_end = max(max_end, left.max_end)
    if right is not None:
        max_end = max(max_end, right.max_end)
    count = 1 + (left.count if left is not None else 0) + (right.count if right else 0)
    return IntervalAgg(count, max_end)


class IntervalIndex:
    """当前所有有效 ``[start,end)`` 的集合。"""

    def __init__(self) -> None:
        self._tree = Treap(_interval_singleton, _interval_combine)

    def add(self, start: int, end: int, occupancy_id: str) -> None:
        key = (start, occupancy_id)
        self._tree.upsert(key, lambda: IntervalValue(end, occupancy_id), lambda v: v)

    def remove(self, start: int, end: int, occupancy_id: str) -> None:
        key = (start, occupancy_id)
        self._tree.upsert(key, lambda: IntervalValue(end, occupancy_id), lambda _v: REMOVE)

    def active_at_or_before(self, t: int) -> Set[str]:
        """返回 ``start <= t < end`` 的占用 ID。

        利用子树 ``max_end`` 剪枝：只有 ``max_end <= t`` 时整棵子树才全关闭
        （半开区间在 end 时刻关闭，因此 end == t 不可计入）。
        整体只访问 O(log n + 命中数) 个节点。
        """
        result: Set[str] = set()

        def walk(node: Optional[TreapNode]) -> None:
            if node is None:
                return
            value: IntervalValue = node.value
            agg: IntervalAgg = node.agg
            if agg.max_end <= t:
                return  # 整棵子树的 end 都已关闭
            if node.left is not None:
                walk(node.left)
            start, occupancy_id = node.key
            if start <= t < value.end:
                result.add(occupancy_id)
            walk(node.right)

        walk(self._tree.root)
        return result

    def all_intervals(self):
        for (start, occupancy_id), value in self._tree.in_order():
            yield start, value.end, occupancy_id
