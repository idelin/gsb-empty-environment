"""宽阶段：为每个对象维护时间窗上的时空包围盒，sweep-and-prune 产出候选对。

包围盒是轨迹在 [t0, t1] 上扫掠体积的轴对齐包围（闭合），
候选判定用闭区间相交；是否真碰撞由窄阶段精确判定。
"""

from __future__ import annotations

from fractions import Fraction

from trajectory import Trajectory


class SweptBound:
    __slots__ = ("lo", "hi", "t0", "t1")

    def __init__(self, lo, hi, t0, t1):
        self.lo = lo
        self.hi = hi
        self.t0 = t0
        self.t1 = t1

    def intersects(self, other) -> bool:
        if self.t1 < other.t0 or other.t1 < self.t0:
            return False
        return all(
            self.lo[k] <= other.hi[k] and other.lo[k] <= self.hi[k]
            for k in range(len(self.lo))
        )


def swept_bound(traj: Trajectory, size, t0, t1):
    """轨迹在 [t0, t1] 内的时空包围盒；窗口为空返回 None。"""
    t0 = Fraction(t0)
    t1 = Fraction(t1)
    start = max(t0, traj.birth)
    if t1 <= start:
        return None
    points = [start, t1]
    for seg in traj.segments:
        if start < seg.start < t1:
            points.append(seg.start)
    lo = list(traj.position_at(start))
    hi = [lo[k] + size[k] for k in range(traj.dim)]
    for p in points:
        pos = traj.position_at(p)
        for k in range(traj.dim):
            if pos[k] < lo[k]:
                lo[k] = pos[k]
            if pos[k] + size[k] > hi[k]:
                hi[k] = pos[k] + size[k]
    return SweptBound(tuple(lo), tuple(hi), start, t1)


class BroadPhaseIndex:
    """对象 id -> SweptBound 的索引；candidates() 做 x 轴 sweep-and-prune。"""

    def __init__(self):
        self._bounds = {}

    def upsert(self, oid, bound: SweptBound):
        self._bounds[oid] = bound

    def remove(self, oid):
        self._bounds.pop(oid, None)

    def candidates(self):
        items = sorted(self._bounds.items(), key=lambda kv: (kv[1].lo[0], repr(kv[0])))
        result = []
        for i, (oid_a, bound_a) in enumerate(items):
            for oid_b, bound_b in items[i + 1:]:
                if bound_b.lo[0] > bound_a.hi[0]:
                    break
                if bound_a.intersects(bound_b):
                    result.append((oid_a, oid_b))
        return result
