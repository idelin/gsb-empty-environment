"""版本化时间线：局部修订产生新版本，旧版本查询不受影响。

映射对象不可变；insert_pause 在 [start, end) 上插入暂停段，
并把其后的段整体平移，保持逻辑时间连续。
"""
from __future__ import annotations

from .intervals import Interval
from .mapping import PiecewiseAffineMap, Segment
from .quantities import TimePoint, WallClock


class VersionedTimeline:
    def __init__(self, base: PiecewiseAffineMap):
        self._versions = [base]

    def version(self, index=None) -> PiecewiseAffineMap:
        return self._versions[-1 if index is None else index]

    @property
    def version_count(self) -> int:
        return len(self._versions)

    def insert_pause(self, start: WallClock, end: WallClock, seg_id=None) -> int:
        """在 [start, end) 插入暂停段，返回新版本号。

        暂停期间逻辑时间不前进：5 和 10 会映射到同一逻辑点。
        暂停起点处由暂停段接管（[start, end) 左闭右开）。
        """
        s, e = start.value, end.value
        if not s < e:
            raise ValueError("pause start must be before end")
        old = self._versions[-1]
        f_s = old.value_at(s)
        f_e = old.value_at(e)
        if f_e is None:
            for seg in old.segments:
                if seg.domain.hi == e:
                    f_e = seg.apply(e)  # e 处右极限
                    break
        if f_s is None or f_e is None:
            raise ValueError("pause range outside mapped domain")
        delta = f_e - f_s  # 暂停期间被抹去的逻辑时长
        new_segments = []
        for seg in old.segments:
            d = seg.domain
            if d.hi < s or (d.hi == s and not d.hi_closed):
                new_segments.append(seg)
            elif d.hi == s and d.hi_closed:
                if d.lo < s:  # 端点 s 交给暂停段
                    new_segments.append(
                        Segment(Interval(d.lo, s, d.lo_closed, False),
                                seg.rate, seg.offset,
                                seg.seg_id + "|pre", seg.kind, seg.parents)
                    )
            elif d.lo >= e:
                new_segments.append(
                    Segment(d, seg.rate, seg.offset - delta,
                            seg.seg_id, seg.kind, seg.parents)
                )
            else:  # 与 [s, e) 相交，需要切分
                if d.lo < s:
                    new_segments.append(
                        Segment(Interval(d.lo, s, d.lo_closed, False),
                                seg.rate, seg.offset,
                                seg.seg_id + "|pre", seg.kind, seg.parents)
                    )
                if d.hi > e or (d.hi == e and d.hi_closed):
                    new_segments.append(
                        Segment(Interval(e, d.hi, True, d.hi_closed),
                                seg.rate, seg.offset - delta,
                                seg.seg_id + "|post", seg.kind, seg.parents)
                    )
        pause_id = seg_id or f"pause[{s},{e})"
        new_segments.append(
            Segment(Interval(s, e, True, False), 0, f_s, pause_id, "pause")
        )
        self._versions.append(PiecewiseAffineMap(new_segments))
        return len(self._versions) - 1

    def logical_at(self, wc: WallClock, version=None) -> TimePoint:
        val = self.version(version).value_at(wc.value)
        if val is None:
            raise KeyError(f"no segment covers wall-clock {wc.value}")
        return TimePoint(val)

    def image(self, intervals, version=None):
        return self.version(version).image(intervals)

    def preimage(self, intervals, version=None):
        return self.version(version).preimage(intervals)
