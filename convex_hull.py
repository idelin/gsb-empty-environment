"""动态点集凸包维护与极值查询（Python 3.11，仅标准库，精确算术）。

设计约定（所有退化情形均有明确定义，无任何 epsilon 修补）：

- 坐标只接受 ``int`` 或 ``fractions.Fraction``，内部统一为 ``Fraction``，
  所有拓扑判断（转向、共线、极值比较）均为精确有理数运算。
  传入 ``float``/``bool`` 会直接抛出 ``TypeError``，避免隐式精度损失。
- 同一个几何坐标允许绑定多个 ID（``coord -> [ids]``）。
  删除某个 ID 只影响该 ID；只有当某坐标的最后一个 ID 被删除时，
  该几何点才从点集中消失。
- 边上共线点保留策略固定为「保留」：凸包结果按逆时针给出，
  边上的共线点按顺序包含在结果链中。
- 退化情形：
  * 空集      -> 凸包为 []，极值查询抛 ``ValueError``；
  * 单点      -> 凸包为 [p]；
  * 两点      -> 凸包为有序链 [p1, p2]（按 (x, y) 字典序）；
  * 全部共线  -> 凸包为从一端到另一端的有序链（含中间所有点，
                 不往返重复）。
- 插入严格内部点不改变包络（由单调链算法自然保证）。

复杂度：点坐标维护在有序数组中，插入/删除 O(n)；每次变更后
用单调链（Andrew monotone chain）O(n) 重建凸包；极值查询 O(h)。
"""

from __future__ import annotations

from bisect import bisect_left, insort
from fractions import Fraction
from typing import Hashable, Iterator, Union

Number = Union[int, Fraction]
Coord = tuple  # (Fraction, Fraction)

__all__ = ["DynamicConvexHull", "Number", "Coord"]


def _to_exact(value: Number) -> Fraction:
    """把 int/Fraction 转成精确的 Fraction，拒绝其它类型。"""
    if isinstance(value, bool):
        raise TypeError("bool 不是合法坐标，请使用 int 或 Fraction")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, Fraction):
        return value
    raise TypeError(
        f"坐标必须是 int 或 Fraction，收到 {type(value).__name__!r}；"
        "float 会破坏精确性，请用 Fraction('0.1') 之类的方式构造"
    )


def _cross(o: Coord, a: Coord, b: Coord) -> Fraction:
    """叉积 (a - o) x (b - o)，精确值；>0 左转，<0 右转，=0 共线。"""
    return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0])


class DynamicConvexHull:
    """支持插入、删除、重复坐标多 ID 的动态凸包。"""

    def __init__(self) -> None:
        self._id_to_coord: dict[Hashable, Coord] = {}
        self._coord_to_ids: dict[Coord, list[Hashable]] = {}
        self._sorted_coords: list[Coord] = []  # 去重后按 (x, y) 升序
        self._hull: list[Coord] = []           # 缓存的凸包链

    # ------------------------------------------------------------------
    # 基本查询
    # ------------------------------------------------------------------
    def __len__(self) -> int:
        """当前点集中的 ID 数量。"""
        return len(self._id_to_coord)

    def __contains__(self, pid: Hashable) -> bool:
        return pid in self._id_to_coord

    def __iter__(self) -> Iterator[Hashable]:
        return iter(self._id_to_coord)

    def coord_of(self, pid: Hashable) -> Coord:
        """返回某 ID 绑定的坐标 (Fraction, Fraction)。"""
        return self._id_to_coord[pid]

    def ids_at(self, x: Number, y: Number) -> tuple:
        """返回某坐标当前绑定的所有 ID（按插入顺序）。"""
        coord = (_to_exact(x), _to_exact(y))
        return tuple(self._coord_to_ids.get(coord, ()))

    # ------------------------------------------------------------------
    # 修改操作
    # ------------------------------------------------------------------
    def add(self, pid: Hashable, x: Number, y: Number) -> None:
        """加入点 (x, y) 并绑定 ID；ID 已存在时抛 ValueError。"""
        if pid in self._id_to_coord:
            raise ValueError(f"ID 已存在: {pid!r}")
        coord = (_to_exact(x), _to_exact(y))
        self._id_to_coord[pid] = coord
        ids = self._coord_to_ids.get(coord)
        if ids is None:
            self._coord_to_ids[coord] = [pid]
            insort(self._sorted_coords, coord)
        else:
            ids.append(pid)
        self._rebuild()

    def remove(self, pid: Hashable) -> Coord:
        """删除一个 ID；只有坐标的最后一个 ID 被删时几何点才消失。"""
        try:
            coord = self._id_to_coord.pop(pid)
        except KeyError:
            raise ValueError(f"ID 不存在: {pid!r}") from None
        ids = self._coord_to_ids[coord]
        ids.remove(pid)
        if not ids:
            del self._coord_to_ids[coord]
            idx = bisect_left(self._sorted_coords, coord)
            self._sorted_coords.pop(idx)
        self._rebuild()
        return coord

    # ------------------------------------------------------------------
    # 凸包与来源映射
    # ------------------------------------------------------------------
    def hull(self) -> list[Coord]:
        """凸包坐标链。

        一般情形：逆时针有序，边上共线点按顺序保留，首尾不重复。
        全共线/两点/单点：见模块 docstring 的退化定义。
        """
        return list(self._hull)

    def hull_with_ids(self) -> list[tuple[Coord, tuple]]:
        """凸包链 + 来源映射：每个坐标附带该处的所有 ID。"""
        return [(c, tuple(self._coord_to_ids[c])) for c in self._hull]

    # ------------------------------------------------------------------
    # 极值查询
    # ------------------------------------------------------------------
    def extreme(self, dx: Number, dy: Number) -> tuple[Fraction, list[tuple[Coord, tuple]]]:
        """最大化 dx*x + dy*y，返回 (最大值, [(坐标, IDs), ...])。

        方向 (dx, dy) 不能为零向量。若最大值在整条边上取得（方向
        与某边垂直），由于共线点保留策略，返回该边上所有点及其 ID。
        空点集抛 ValueError。比较全部为精确运算。
        """
        dx, dy = _to_exact(dx), _to_exact(dy)
        if dx == 0 and dy == 0:
            raise ValueError("方向向量不能为零")
        if not self._hull:
            raise ValueError("空点集没有极值")
        best_value: Fraction | None = None
        best_coords: list[Coord] = []
        for c in self._hull:
            value = dx * c[0] + dy * c[1]
            if best_value is None or value > best_value:
                best_value = value
                best_coords = [c]
            elif value == best_value:
                best_coords.append(c)
        return best_value, [(c, tuple(self._coord_to_ids[c])) for c in best_coords]

    # ------------------------------------------------------------------
    # 内部：单调链重建（输入已有序，O(n)）
    # ------------------------------------------------------------------
    def _rebuild(self) -> None:
        pts = self._sorted_coords
        n = len(pts)
        if n <= 2:
            self._hull = list(pts)
            return
        # 全共线：返回从一端到另一端的有序链（含中间点，不往返）。
        first, last = pts[0], pts[-1]
        if all(_cross(first, last, p) == 0 for p in pts):
            self._hull = list(pts)
            return
        # 单调链；只在严格右转时弹栈 => 边上共线点保留。
        lower: list[Coord] = []
        for p in pts:
            while len(lower) >= 2 and _cross(lower[-2], lower[-1], p) < 0:
                lower.pop()
            lower.append(p)
        upper: list[Coord] = []
        for p in reversed(pts):
            while len(upper) >= 2 and _cross(upper[-2], upper[-1], p) < 0:
                upper.pop()
            upper.append(p)
        self._hull = lower[:-1] + upper[:-1]


if __name__ == "__main__":
    # 运行样例：构造 -> 查询 -> 删除 -> 再查询
    h = DynamicConvexHull()
    h.add("A", 0, 0)
    h.add("B", 4, 0)
    h.add("C", 4, 4)
    h.add("D", 0, 4)
    h.add("E", 2, 0)            # 底边共线点，按策略保留
    h.add("F", 2, 2)            # 严格内部点，不改变包络
    h.add("G", Fraction(1, 3), Fraction(1, 3))  # 对角线上的有理点，精确共线
    h.add("B2", 4, 0)           # 与 B 同坐标的第二个 ID

    print("凸包（含来源映射）:")
    for coord, ids in h.hull_with_ids():
        print(f"  {coord} <- {ids}")

    value, winners = h.extreme(1, 1)
    print(f"\n方向 (1, 1) 最大值 = {value}, 取得于 {winners}")
    value, winners = h.extreme(0, -1)
    print(f"方向 (0,-1) 最大值 = {value}, 取得于（整条底边）{winners}")

    print("\n删除 B（坐标 (4,0) 仍有 B2，几何点保留）...")
    h.remove("B")
    print("  (4,0) 处 ID:", h.ids_at(4, 0))
    print("删除 B2（该坐标几何点消失）...")
    h.remove("B2")
    print("  凸包:", h.hull())

    print("\n删除内部点 F，包络不变:", h.hull() == [c for c, _ in h.hull_with_ids()])
