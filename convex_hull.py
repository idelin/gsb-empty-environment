"""动态点集凸包维护与极值查询（精确算术，无 epsilon）。

特性：
- 支持点的插入与删除；同一几何坐标可挂载多个 ID。
- 坐标接受 int、fractions.Fraction 或 (分子, 分母) 元组，内部统一为 Fraction，
  所有拓扑判断（方向/共线）均为精确有理数运算，绝不使用 epsilon。
- 固定共线策略：凸包边上的共线点保留在包络中，按边界顺序输出。
- 退化情形定义：
    * 空集        -> hull() 返回 []
    * 单点        -> 返回该点本身
    * 全部共线    -> 返回从一端到另一端的有序链（线段，含中间共线点）
- 极值查询：给定方向 d，返回使 dot(p, d) 最大的包络点；
  平局（方向垂直于某条边）时返回达到最大值的全部包络点。
"""

from __future__ import annotations

from fractions import Fraction
from typing import Dict, Hashable, Iterator, List, NamedTuple, Tuple

__all__ = ["DynamicConvexHull", "HullVertex", "ExtremeResult"]


def _to_fraction(value) -> Fraction:
    """把 int / Fraction / (num, den) 元组精确转换为 Fraction。

    拒绝 float 等不精确类型，避免隐式引入误差。
    """
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool 不是合法坐标")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, tuple) and len(value) == 2:
        num, den = value
        if isinstance(num, int) and isinstance(den, int) and not isinstance(num, bool) and not isinstance(den, bool):
            return Fraction(num, den)
    raise TypeError(f"坐标必须是 int、Fraction 或 (num, den) 元组，得到 {type(value).__name__}")


class HullVertex(NamedTuple):
    """包络上的一个几何点及其来源 ID 映射。"""

    x: Fraction
    y: Fraction
    ids: Tuple[Hashable, ...]  # 该坐标上当前存活的全部 ID（按插入顺序）


class ExtremeResult(NamedTuple):
    """极值查询结果：最大值与取得该值的全部包络点。"""

    value: Fraction
    vertices: Tuple[HullVertex, ...]


def _cross(o: Tuple[Fraction, Fraction], a: Tuple[Fraction, Fraction], b: Tuple[Fraction, Fraction]) -> Fraction:
    """精确叉积 (a - o) x (b - o)。"""
    return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0])


class DynamicConvexHull:
    """支持增删的动态凸包。

    实现说明：修改仅打脏标记，查询时按需用单调链（Andrew）算法 O(n log n) 重建。
    弹出条件为严格叉积 < 0，因此边上的共线点被保留在包络中（固定策略）。
    """

    def __init__(self) -> None:
        self._id_to_point: Dict[Hashable, Tuple[Fraction, Fraction]] = {}
        self._point_to_ids: Dict[Tuple[Fraction, Fraction], Dict[Hashable, None]] = {}
        self._hull_cache: List[Tuple[Fraction, Fraction]] | None = None

    # ------------------------------------------------------------------ 修改

    def add(self, point_id: Hashable, x, y) -> None:
        """插入点。point_id 已存在时抛 ValueError（固定策略）。"""
        if point_id in self._id_to_point:
            raise ValueError(f"ID 已存在: {point_id!r}")
        point = (_to_fraction(x), _to_fraction(y))
        self._id_to_point[point_id] = point
        self._point_to_ids.setdefault(point, {})[point_id] = None
        self._hull_cache = None

    def remove(self, point_id: Hashable) -> bool:
        """删除指定 ID。只移除该 ID；同坐标的其他 ID 保留时几何点不消失。

        返回是否实际删除（ID 不存在返回 False）。
        """
        point = self._id_to_point.pop(point_id, None)
        if point is None:
            return False
        ids = self._point_to_ids[point]
        del ids[point_id]
        if not ids:
            del self._point_to_ids[point]
        self._hull_cache = None
        return True

    # ------------------------------------------------------------------ 查询

    def __len__(self) -> int:
        """当前存活的 ID 总数。"""
        return len(self._id_to_point)

    def num_distinct_points(self) -> int:
        """当前存活的不同几何坐标数。"""
        return len(self._point_to_ids)

    def __contains__(self, point_id: Hashable) -> bool:
        return point_id in self._id_to_point

    def ids_at(self, x, y) -> Tuple[Hashable, ...]:
        """查询某坐标上存活的全部 ID（来源映射）。"""
        point = (_to_fraction(x), _to_fraction(y))
        return tuple(self._point_to_ids.get(point, ()))

    def hull(self) -> List[HullVertex]:
        """返回凸包，逆时针序，含边上共线点；附带每个几何点的来源 ID。

        退化：空 -> []；单点 -> 单元素；全共线 -> 有序链（单向，不往返）。
        """
        points = self._hull_points()
        return [HullVertex(x, y, tuple(self._point_to_ids[(x, y)])) for (x, y) in points]

    def extreme(self, dx, dy) -> ExtremeResult:
        """沿方向 (dx, dy) 求最大点积。平局返回全部达到最大值的包络点。"""
        direction = (_to_fraction(dx), _to_fraction(dy))
        if direction == (Fraction(0), Fraction(0)):
            raise ValueError("方向不能为零向量")
        points = self._hull_points()
        if not points:
            raise ValueError("空点集没有极值点")
        best: Fraction | None = None
        winners: List[HullVertex] = []
        for point in points:
            value = direction[0] * point[0] + direction[1] * point[1]
            if best is None or value > best:
                best = value
                winners = [point]
            elif value == best:
                winners.append(point)
        return ExtremeResult(
            best,
            tuple(HullVertex(x, y, tuple(self._point_to_ids[(x, y)])) for (x, y) in winners),
        )

    # ------------------------------------------------------------------ 内部

    def _hull_points(self) -> List[Tuple[Fraction, Fraction]]:
        if self._hull_cache is None:
            self._hull_cache = self._build_hull()
        return list(self._hull_cache)

    def _build_hull(self) -> List[Tuple[Fraction, Fraction]]:
        points = sorted(self._point_to_ids)
        n = len(points)
        if n <= 1:
            return points

        first, last = points[0], points[-1]
        if all(_cross(first, last, p) == 0 for p in points[1:-1]):
            # 全共线：退化为线段，返回从一端到另一端的有序链。
            return points

        # 单调链，严格 < 0 才弹出 => 边上共线点保留。
        lower: List[Tuple[Fraction, Fraction]] = []
        for p in points:
            while len(lower) >= 2 and _cross(lower[-2], lower[-1], p) < 0:
                lower.pop()
            lower.append(p)
        upper: List[Tuple[Fraction, Fraction]] = []
        for p in reversed(points):
            while len(upper) >= 2 and _cross(upper[-2], upper[-1], p) < 0:
                upper.pop()
            upper.append(p)
        return lower[:-1] + upper[:-1]

    def __iter__(self) -> Iterator[HullVertex]:
        return iter(self.hull())

    def __repr__(self) -> str:
        return f"DynamicConvexHull(ids={len(self)}, distinct={self.num_distinct_points()})"
