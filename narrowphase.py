"""窄阶段：两条分段匀速轨迹 + 盒尺寸，精确求时间窗内的全部碰撞区间。

显式规则（无 epsilon）：
- 碰撞 = 所有坐标轴上区间严格重叠（正长度交集），即正体积重叠。
- 瞬时相切（仅在某孤立时刻接触）不构成碰撞。
- 持续贴面滑动（某轴交集恒为单点）同样不构成碰撞。
- 区间按开区间语义计算；相邻分段边界上的区间在报告时合并，
  合并点处的接触可能是瞬时的（见 README）。
"""

from __future__ import annotations

from fractions import Fraction

from trajectory import Trajectory


def _axis_overlap(lo1, hi1, v1, lo2, hi2, v2, span):
    """在 s ∈ [0, span] 上求 lo1+v1*s < hi2+v2*s 且 lo2+v2*s < hi1+v1*s 的解集。

    返回 (lo, hi) 开区间（Fraction），无解或退化为单点时返回 None。
    每条不等式形如 c*s < d，精确求解。
    """
    lo = Fraction(0)
    hi = span
    for c, d in ((v1 - v2, hi2 - lo1), (v2 - v1, hi1 - lo2)):
        if c == 0:
            if d <= 0:
                return None
        elif c > 0:  # s < d/c
            bound = d / c
            if bound <= lo:
                return None
            if bound < hi:
                hi = bound
        else:  # s > d/c
            bound = d / c
            if bound >= hi:
                return None
            if bound > lo:
                lo = bound
    if lo < hi:
        return (lo, hi)
    return None


def _piece_overlap(seg_a, size_a, seg_b, size_b, p, q):
    """两条轨迹在 [p, q] 上各自匀速，求盒重叠的绝对时间开区间或 None。"""
    pos_a = seg_a.position_at(p)
    pos_b = seg_b.position_at(p)
    span = q - p
    lo = Fraction(0)
    hi = span
    for k in range(len(pos_a)):
        iv = _axis_overlap(
            pos_a[k], pos_a[k] + size_a[k], seg_a.vel[k],
            pos_b[k], pos_b[k] + size_b[k], seg_b.vel[k],
            span,
        )
        if iv is None:
            return None
        axis_lo, axis_hi = iv
        if axis_lo > lo:
            lo = axis_lo
        if axis_hi < hi:
            hi = axis_hi
        if lo >= hi:
            return None
    return (p + lo, p + hi)


def collision_intervals(traj_a: Trajectory, size_a, traj_b: Trajectory, size_b, t0, t1):
    """返回 (t0, t1) 内全部碰撞区间 [(start, end), ...]，按时间升序、已合并。

    窗口为空（t1 <= t0）或对象尚未出生时不产生任何区间。
    """
    t0 = Fraction(t0)
    t1 = Fraction(t1)
    if t1 <= t0:
        return []
    start = max(t0, traj_a.birth, traj_b.birth)
    if start >= t1:
        return []
    breakpoints = {start, t1}
    for traj in (traj_a, traj_b):
        for seg in traj.segments:
            if start < seg.start < t1:
                breakpoints.add(seg.start)
    points = sorted(breakpoints)
    intervals = []
    for p, q in zip(points, points[1:]):
        seg_a = traj_a.segment_at(p)
        seg_b = traj_b.segment_at(p)
        iv = _piece_overlap(seg_a, size_a, seg_b, size_b, p, q)
        if iv is None:
            continue
        if intervals and iv[0] <= intervals[-1][1]:
            prev_lo, prev_hi = intervals[-1]
            intervals[-1] = (prev_lo, max(prev_hi, iv[1]))
        else:
            intervals.append(iv)
    return intervals


def first_begin(traj_a, size_a, traj_b, size_b, t, t1, include_start):
    """窗口 [t, t1] 内首个碰撞区间的开始时刻；没有则 None。

    include_start=False 时排除恰好从 t 开始的区间（用于事件处理后的再预测，
    避免重复报告同一碰撞）。
    """
    for iv_start, _ in collision_intervals(traj_a, size_a, traj_b, size_b, t, t1):
        if include_start or iv_start > t:
            return iv_start
    return None
