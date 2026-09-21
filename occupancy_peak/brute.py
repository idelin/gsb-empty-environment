"""逐端点扫描的参考实现：用于在小数据上核验增量索引的结果。"""
from __future__ import annotations

from typing import Any, Dict, Tuple

from .tracker import PeakResult, PeakSegment


def brute_peak(intervals: Dict[Any, Tuple[float, float]], qs, qe) -> PeakResult:
    """把窗口 [qs, qe) 按所有端点切成基本段，逐段数覆盖数，取峰值。

    复杂度 O(n^2) 级别，只作为正确性基准，不用于生产路径。
    """
    if not qs < qe:
        raise ValueError(f"查询范围必须满足 qs < qe，收到 [{qs}, {qe})")
    points = {qs, qe}
    for s, e in intervals.values():
        if qs < s < qe:
            points.add(s)
        if qs < e < qe:
            points.add(e)
    pts = sorted(points)
    best = 0
    segs = []  # [start, end, set(ids)]
    for a, b in zip(pts, pts[1:]):
        ids = {oid for oid, (s, e) in intervals.items() if s < b and e > a}
        c = len(ids)
        if c > best:
            best = c
            segs = [[a, b, set(ids)]]
        elif c == best and c > 0:
            if segs and segs[-1][1] == a:
                segs[-1][1] = b
                segs[-1][2] |= ids
            else:
                segs.append([a, b, set(ids)])
    return PeakResult(
        best, tuple(PeakSegment(a, b, frozenset(ids)) for a, b, ids in segs)
    )
