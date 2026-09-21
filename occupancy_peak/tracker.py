"""从可能乱序的 start/end 记录重建设备占用区间，并查询峰值并发。"""
from __future__ import annotations

from dataclasses import dataclass
from enum import Enum
from typing import Any, Dict, Optional, Tuple

from .index import SweepIndex


class Status(Enum):
    RECORDED = "recorded"                  # 新孤立端点已登记，等待配对
    PAIRED = "paired"                      # 配对完成，半开区间已建立并入库
    CORRECTED = "corrected"                # 端点更正已生效
    DUPLICATE = "duplicate"                # 相同端点重复到达，幂等忽略
    REJECTED_INVALID = "rejected_invalid"  # 会导致 end <= start，已拒绝，原状态不变
    RETRACTED = "retracted"                # 占用记录已撤回
    NOT_FOUND = "not_found"                # 目标 ID 不存在


@dataclass(frozen=True)
class Outcome:
    """每次写入的明确结果。"""
    status: Status
    occ_id: Any
    interval: Optional[Tuple[float, float]] = None  # 操作生效后的区间（若已配对）
    detail: str = ""


@dataclass(frozen=True)
class PeakSegment:
    """一段达到峰值的最大连续时段，ids 为证据：该时段内有贡献的占用 ID。"""
    start: float
    end: float
    ids: frozenset


@dataclass(frozen=True)
class PeakResult:
    peak: int
    segments: Tuple[PeakSegment, ...]


class _Record:
    __slots__ = ("start", "end")

    def __init__(self, start=None, end=None):
        self.start = start
        self.end = end


class OccupancyTracker:
    """占用记录的状态机 + 增量峰值索引。

    不变式：同一 ID 两个端点都齐时必有 end > start，此时区间生效并进入
    索引；任何会使 end <= start 的端点都会被拒绝且不改变原状态，因此
    绝不产生零长 / 负长度的虚假占用。孤立端点（只有 start 或只有 end）
    不进入索引，只出现在 orphans() 中。
    """

    def __init__(self) -> None:
        self._recs: Dict[Any, _Record] = {}
        self._intervals: Dict[Any, Tuple[float, float]] = {}
        self._index = SweepIndex()

    # ---- 写入 ----

    def record_start(self, occ_id, t) -> Outcome:
        rec = self._recs.get(occ_id)
        if rec is None:
            self._recs[occ_id] = _Record(start=t)
            return Outcome(Status.RECORDED, occ_id, detail="start 已登记，等待 end")
        if rec.start == t:
            return Outcome(Status.DUPLICATE, occ_id, self._intervals.get(occ_id),
                           "相同 start 重复到达，忽略")
        if rec.end is not None:
            if rec.end <= t:
                return Outcome(Status.REJECTED_INVALID, occ_id, self._intervals.get(occ_id),
                               f"start={t} 不早于 end={rec.end}，拒绝以保留原状态")
            if rec.start is not None:
                old = rec.start
                self._index.remove_interval(old, rec.end)
                rec.start = t
                self._index.add_interval(t, rec.end)
                self._intervals[occ_id] = (t, rec.end)
                return Outcome(Status.CORRECTED, occ_id, (t, rec.end),
                               f"start 由 {old} 更正为 {t}")
            rec.start = t
            self._index.add_interval(t, rec.end)
            self._intervals[occ_id] = (t, rec.end)
            return Outcome(Status.PAIRED, occ_id, (t, rec.end), "配对完成")
        rec.start = t
        return Outcome(Status.CORRECTED, occ_id, detail="孤立 start 已更正")

    def record_end(self, occ_id, t) -> Outcome:
        rec = self._recs.get(occ_id)
        if rec is None:
            self._recs[occ_id] = _Record(end=t)
            return Outcome(Status.RECORDED, occ_id, detail="end 已登记，等待 start")
        if rec.end == t:
            return Outcome(Status.DUPLICATE, occ_id, self._intervals.get(occ_id),
                           "相同 end 重复到达，忽略")
        if rec.start is not None:
            if t <= rec.start:
                return Outcome(Status.REJECTED_INVALID, occ_id, self._intervals.get(occ_id),
                               f"end={t} 不晚于 start={rec.start}，拒绝以保留原状态")
            if rec.end is not None:
                old = rec.end
                self._index.remove_interval(rec.start, old)
                rec.end = t
                self._index.add_interval(rec.start, t)
                self._intervals[occ_id] = (rec.start, t)
                return Outcome(Status.CORRECTED, occ_id, (rec.start, t),
                               f"end 由 {old} 更正为 {t}")
            rec.end = t
            self._index.add_interval(rec.start, t)
            self._intervals[occ_id] = (rec.start, t)
            return Outcome(Status.PAIRED, occ_id, (rec.start, t), "配对完成")
        rec.end = t
        return Outcome(Status.CORRECTED, occ_id, detail="孤立 end 已更正")

    def retract(self, occ_id) -> Outcome:
        rec = self._recs.pop(occ_id, None)
        if rec is None:
            return Outcome(Status.NOT_FOUND, occ_id, detail="无此占用记录")
        iv = self._intervals.pop(occ_id, None)
        if iv is not None:
            self._index.remove_interval(*iv)
        return Outcome(Status.RETRACTED, occ_id, iv, "占用记录已撤回")

    # ---- 查询 ----

    def interval(self, occ_id) -> Optional[Tuple[float, float]]:
        return self._intervals.get(occ_id)

    def intervals(self) -> Dict[Any, Tuple[float, float]]:
        return dict(self._intervals)

    def orphans(self) -> Dict[Any, Dict[str, Any]]:
        """孤立记录：只有单一端点、尚未配对的占用。不进入区间索引。"""
        return {
            oid: {"start": rec.start, "end": rec.end}
            for oid, rec in self._recs.items()
            if oid not in self._intervals
        }

    def level_at(self, t) -> int:
        return self._index.level_at(t)

    def peak(self, qs, qe) -> PeakResult:
        """窗口 [qs, qe) 内的峰值并发与所有达到峰值的时段（含贡献 ID 证据）。"""
        if not qs < qe:
            raise ValueError(f"查询范围必须满足 qs < qe，收到 [{qs}, {qe})")
        best, raw = self._index.peak_segments(qs, qe)
        segments = tuple(
            PeakSegment(a, b, self._contributors(a, b)) for a, b in raw
        )
        return PeakResult(best, segments)

    def _contributors(self, a, b) -> frozenset:
        return frozenset(
            oid for oid, (s, e) in self._intervals.items() if s < b and e > a
        )
