"""参考实现：小规模全对、逐段笛卡尔积的朴素解析解，用于对照测试。

与 narrowphase 的断点扫描不同，这里对两条轨迹的段做笛卡尔积，
每对段在时间段交集上独立求解，最后合并。结构刻意保持朴素。
"""

from __future__ import annotations

from fractions import Fraction

INF = Fraction(10**30)  # 足够大的“无穷”，所有测试窗口都远小于它


def _segment_spans(traj, t1):
    """每段的 [start, end) 时间范围，最后一段 end 取 t1。"""
    spans = []
    segs = traj.segments
    for i, seg in enumerate(segs):
        end = segs[i + 1].start if i + 1 < len(segs) else t1
        spans.append((seg, seg.start, end))
    return spans


def _piece_overlap_brute(seg_a, size_a, seg_b, size_b, lo, hi):
    """两匀速段在 [lo, hi] 上的重叠开区间（绝对时间），无则 None。"""
    span = hi - lo
    if span <= 0:
        return None
    pa = seg_a.position_at(lo)
    pb = seg_b.position_at(lo)
    t_lo = Fraction(0)
    t_hi = span
    for k in range(len(pa)):
        # 不等式：pa+va*s 与 pb+vb*s 两区间严格相交
        for c, d in (
            (seg_a.vel[k] - seg_b.vel[k], (pb[k] + size_b[k]) - pa[k]),
            (seg_b.vel[k] - seg_a.vel[k], (pa[k] + size_a[k]) - pb[k]),
        ):
            if c == 0:
                if d <= 0:
                    return None
            elif c > 0:
                bound = d / c
                if bound <= t_lo:
                    return None
                t_hi = min(t_hi, bound)
            else:
                bound = d / c
                if bound >= t_hi:
                    return None
                t_lo = max(t_lo, bound)
    if t_lo < t_hi:
        return (lo + t_lo, lo + t_hi)
    return None


def reference_pair_intervals(traj_a, size_a, traj_b, size_b, t0, t1):
    """全段对枚举的碰撞区间；返回与 narrowphase 相同的合并区间列表。"""
    t0 = Fraction(t0)
    t1 = Fraction(t1)
    if t1 <= t0:
        return []
    start = max(t0, traj_a.birth, traj_b.birth)
    if start >= t1:
        return []
    raw = []
    for seg_a, a_lo, a_hi in _segment_spans(traj_a, t1):
        for seg_b, b_lo, b_hi in _segment_spans(traj_b, t1):
            lo = max(a_lo, b_lo, start)
            hi = min(a_hi, b_hi, t1)
            iv = _piece_overlap_brute(seg_a, size_a, seg_b, size_b, lo, hi)
            if iv is not None:
                raw.append(iv)
    raw.sort()
    merged = []
    for lo, hi in raw:
        if merged and lo <= merged[-1][1]:
            merged[-1] = (merged[-1][0], max(merged[-1][1], hi))
        else:
            merged.append((lo, hi))
    return merged


def reference_all_pairs(world, t0, t1):
    """对 world 中全部对象对（无视宽阶段）求碰撞区间。"""
    t0 = Fraction(t0)
    t1 = Fraction(t1)
    result = {}
    ids = sorted(world._objects, key=repr)
    for i, a in enumerate(ids):
        oa = world._objects[a]
        for b in ids[i + 1:]:
            ob = world._objects[b]
            lo, hi_a_end = world._window_of(oa, t0, t1)
            _, hi_b_end = world._window_of(ob, t0, t1)
            hi = min(hi_a_end, hi_b_end)
            ivs = reference_pair_intervals(oa.traj, oa.size, ob.traj, ob.size, lo, hi)
            if ivs:
                result[(a, b)] = ivs
    return result
