"""小规模全对分段解析参考实现，用于交叉验证引擎。

与引擎路径相互独立：不使用宽阶段索引与事件队列，而是收集全部
候选时刻（轨迹断点 + 各轴间隙函数的零点），把时间轴剖分为单元，
在每个开单元中点与每个候选点上精确判定闭集相交 / 内部相交，
再把相邻为真的单元合并为碰撞区间。全部为精确有理数运算。
"""

from __future__ import annotations

import itertools

from .world import CollisionInterval


def _gaps(pos_a, size_a, pos_b, size_b):
    """逐轴返回 (a_lo - b_hi, b_lo - a_hi)。"""
    return tuple(
        (pos_a[k] - pos_b[k] - size_b[k], pos_b[k] - pos_a[k] - size_a[k])
        for k in range(len(size_a))
    )


def reference_pair_intervals(traj_a, size_a, traj_b, size_b, t0, t1,
                             destroy_a=None, destroy_b=None):
    hi = t1
    for d in (destroy_a, destroy_b):
        if d is not None:
            hi = min(hi, d)
    lo = t0
    if lo >= hi:
        return []
    clipped = hi < t1  # 被销毁截断：销毁时刻本身不算存在
    cands = {lo, hi}
    cands.update(traj_a.breakpoints(lo, hi))
    cands.update(traj_b.breakpoints(lo, hi))
    bps = sorted(cands)
    roots = set()
    for s, e in zip(bps, bps[1:]):
        if s >= e:
            continue
        ga_s = _gaps(traj_a.position(s), size_a, traj_b.position(s), size_b)
        ga_e = _gaps(traj_a.position(e), size_a, traj_b.position(e), size_b)
        for axis in range(len(size_a)):
            for side in range(2):
                g0 = ga_s[axis][side]
                g1 = ga_e[axis][side]
                if (g0 < 0 < g1) or (g1 < 0 < g0):
                    roots.add(s + (e - s) * g0 / (g0 - g1))
    times = sorted(cands | roots)

    def closed_ok(t):
        return all(g0 <= 0 and g1 <= 0
                   for g0, g1 in _gaps(traj_a.position(t), size_a,
                                       traj_b.position(t), size_b))

    def strict_ok(t):
        return all(g0 < 0 and g1 < 0
                   for g0, g1 in _gaps(traj_a.position(t), size_a,
                                       traj_b.position(t), size_b))

    elements = []  # (start, end, closed, strict)
    for i, t in enumerate(times):
        if t < hi or not clipped:
            elements.append((t, t, closed_ok(t), False))
        if i + 1 < len(times):
            mid = (t + times[i + 1]) / 2
            elements.append((t, times[i + 1], closed_ok(mid), strict_ok(mid)))

    merged = []
    for start, end, ok, strict in elements:
        if not ok:
            continue
        if merged and merged[-1][1] == start:
            m = merged[-1]
            m[1] = end
            if m[2] is None and strict:
                m[2] = start
        else:
            merged.append([start, end, start if strict else None])
    out = []
    for start, end, enter in merged:
        if enter is not None:
            kind = "overlap"
        elif start < end:
            kind = "contact"
        else:
            kind = "touch"
        out.append(CollisionInterval(start, end, kind, enter))
    return out


def reference_intervals(world):
    """对 world 中全部对象对（无索引、全对）计算碰撞区间。"""
    out = {}
    ids = sorted(world.bodies, key=repr)
    for a, b in itertools.combinations(ids, 2):
        ba, bb = world.bodies[a], world.bodies[b]
        ivs = reference_pair_intervals(ba.traj, ba.size, bb.traj, bb.size,
                                       world.t0, world.t1,
                                       ba.t_destroy, bb.t_destroy)
        if ivs:
            out[(a, b)] = ivs
    return out


def reference_first_collision(world):
    best = None
    for pair, ivs in reference_intervals(world).items():
        for iv in ivs:
            if best is None or (iv.start, repr(pair)) < (best[0], repr(best[1])):
                best = (iv.start, pair, iv.kind)
    return best
