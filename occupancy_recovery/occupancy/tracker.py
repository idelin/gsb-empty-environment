"""占用区间重建与峰值并发查询的核心追踪器。

关键不变量：

1. 只有 start/end 配对成功且 ``start < end`` 时，才向索引写入半开区间
   ``[start, end)``；孤立的 start/end 只登记状态，绝不参与并发计数，
   因此孤立的 end 不可能制造"负占用"。
2. 非法更新（例如 ``end <= start``）在触碰索引之前就被拒绝，已有状态
   （包括此前已生效的区间）完全不变。
3. 更正 / 撤回采用"先校验，后对索引做旧区间移除 + 新区间添加"的成对更新，
   外部可观察状态始终一致。
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, List, Optional, Set, Tuple

from .indexes import EventIndex, IntervalIndex
from .records import OccupancyRecord, OperationResult, Outcome, RecordStatus


@dataclass(frozen=True)
class PeakPeriod:
    """一段峰值持续的半开时间区间 ``[start, end)`` 及贡献它的占用 ID。"""

    start: int
    end: Optional[int]
    level: int
    evidence: Tuple[str, ...]


@dataclass(frozen=True)
class PeakResult:
    peak: int
    periods: Tuple[PeakPeriod, ...]
    query_lo: Optional[int]
    query_hi: Optional[int]


class OccupancyTracker:
    def __init__(self) -> None:
        self._records: Dict[str, OccupancyRecord] = {}
        self._events = EventIndex()
        self._intervals = IntervalIndex()

    # ---- 状态只读视图 -------------------------------------------------

    def record(self, occupancy_id: str) -> Optional[OccupancyRecord]:
        return self._records.get(occupancy_id)

    def orphan_records(self) -> List[OccupancyRecord]:
        """只有单端点、尚未形成区间的记录（start-only 或 end-only）。"""
        return [r for r in self._records.values() if r.is_orphan]

    def active_intervals(self) -> List[Tuple[int, int, str]]:
        return list(self._intervals.all_intervals())

    def withdrawn_ids(self) -> List[str]:
        return [oid for oid, r in self._records.items() if r.status is RecordStatus.WITHDRAWN]

    # ---- 端点上报 / 更正 ----------------------------------------------

    def report_start(self, occupancy_id: str, t: int) -> OperationResult:
        return self._report_endpoint(occupancy_id, t, is_start=True)

    def report_end(self, occupancy_id: str, t: int) -> OperationResult:
        return self._report_endpoint(occupancy_id, t, is_start=False)

    def _report_endpoint(
        self, occupancy_id: str, t: int, is_start: bool
    ) -> OperationResult:
        record = self._records.get(occupancy_id)
        if record is None:
            record = OccupancyRecord(occupancy_id)
            self._records[occupancy_id] = record

        if record.status is RecordStatus.WITHDRAWN:
            return OperationResult(
                Outcome.REJECTED_WITHDRAWN,
                occupancy_id,
                "occupancy was withdrawn",
                record.status,
                record.interval_or_none(),
            )

        old_start, old_end = record.start, record.end
        new_start = t if is_start else old_start
        new_end = old_end if is_start else t

        if is_start and old_start == t and record.status in (
            RecordStatus.START_ONLY,
            RecordStatus.PAIRED,
        ):
            return self._duplicate(record, "start already reported at same time")
        if not is_start and old_end == t and record.status in (
            RecordStatus.END_ONLY,
            RecordStatus.PAIRED,
        ):
            return self._duplicate(record, "end already reported at same time")

        # 配对完成必须是合法的半开区间；非法则原子拒绝，状态保持原样。
        if new_start is not None and new_end is not None and new_end <= new_start:
            return OperationResult(
                Outcome.REJECTED_INVALID,
                occupancy_id,
                f"end {new_end} must be strictly greater than start {new_start}",
                record.status,
                record.interval_or_none(),
            )

        corrected = (is_start and old_start is not None) or (
            not is_start and old_end is not None
        )
        was_active = record.is_active

        record.start, record.end = new_start, new_end
        if new_start is not None and new_end is not None:
            record.status = RecordStatus.PAIRED
        elif is_start:
            record.status = RecordStatus.START_ONLY
        else:
            record.status = RecordStatus.END_ONLY

        if was_active:
            # 更正：旧贡献必须全部撤销，再写入新贡献（可能新配对/新位置）。
            self._intervals.remove(old_start, old_end, occupancy_id)
            self._events.remove_interval(old_start, old_end, occupancy_id)
        if record.is_active:
            self._intervals.add(new_start, new_end, occupancy_id)
            self._events.add_interval(new_start, new_end, occupancy_id)

        outcome = Outcome.CORRECTED if corrected else Outcome.RECORDED
        detail = "endpoint corrected" if corrected else (
            "start recorded" if is_start else "end recorded"
        )
        return OperationResult(
            outcome, occupancy_id, detail, record.status, record.interval_or_none()
        )

    @staticmethod
    def _duplicate(record: OccupancyRecord, detail: str) -> OperationResult:
        return OperationResult(
            Outcome.DUPLICATE,
            record.occupancy_id,
            detail,
            record.status,
            record.interval_or_none(),
        )

    # ---- 撤回 ---------------------------------------------------------

    def withdraw(self, occupancy_id: str) -> OperationResult:
        record = self._records.get(occupancy_id)
        if record is None:
            return OperationResult(Outcome.NOT_FOUND, occupancy_id, "no such occupancy")

        if record.status is RecordStatus.WITHDRAWN:
            return OperationResult(
                Outcome.DUPLICATE,
                occupancy_id,
                "already withdrawn",
                record.status,
            )

        if record.is_active:
            self._intervals.remove(record.start, record.end, occupancy_id)
            self._events.remove_interval(record.start, record.end, occupancy_id)

        record.status = RecordStatus.WITHDRAWN
        record.start = None
        record.end = None
        return OperationResult(Outcome.WITHDRAWN, occupancy_id, "withdrawn", record.status)

    # ---- 峰值查询 -----------------------------------------------------

    def peak(self, lo: Optional[int] = None, hi: Optional[int] = None) -> PeakResult:
        """查询半开范围 ``[lo, hi)`` 内的峰值并发及所有达到峰值的时间段。

        ``lo=None`` / ``hi=None`` 表示向左 / 向右无界。每个峰值时段携带
        ``evidence``：该恒定段内真正活跃（``start <= t < end``）的占用 ID。
        扫描器只遍历查询范围内的变化点，不重扫全部区间。
        """
        if lo is not None and hi is not None and hi <= lo:
            raise ValueError("query range [lo, hi) requires hi > lo")

        base = self._events.range_prefix_total(lo) if lo is not None else 0
        # lo 边界点的"先关后开"通过初始活跃集与 base 处理；内部扫描从 lo+1 起。
        scan_lo = None if lo is None else lo + 1
        boundary_delta = self._events.point_total(lo) if lo is not None else 0
        initial_level = base + boundary_delta
        agg = self._events.range_agg(scan_lo, hi)
        if agg is None:
            return self._constant_result(initial_level, lo, hi, lo)

        peak = initial_level
        if agg.count:
            peak = max(peak, initial_level + agg.interior_pmax)
        if peak == 0 and (lo is None or hi is None):
            return PeakResult(0, (), lo, hi)

        periods: List[PeakPeriod] = []
        level = initial_level
        if lo is None:
            active: Set[str] = set()
            segment_start: Optional[int] = None
        else:
            # 证据集合预取 start<=lo 且 end>lo；start==lo 的占用在首个事件点
            # 被重复 add（集合幂等），level 仍严格按 delta 计算。
            active = self._intervals.active_at_or_before(lo)
            segment_start = lo

        for node in self._events.range_nodes(scan_lo, hi):
            t, point = node.key, node.value

            # 在应用 t 的事件前，[segment_start, t) 是一段恒定占用。
            if segment_start is None or t > segment_start:
                self._append_peak_period(
                    periods, segment_start, t, level, peak, active
                )

            # 同一时间点先关闭再打开：[.,t) 与 [t,.) 在交界点不重叠。
            for oid in point.ends:
                active.discard(oid)
            for oid in point.starts:
                active.add(oid)
            level += point.delta
            if level < 0:
                raise RuntimeError("internal error: negative occupancy level")
            segment_start = t

        if segment_start is None:
            segment_start = lo
        self._append_peak_period(periods, segment_start, hi, level, peak, active)
        return PeakResult(peak, tuple(periods), lo, hi)

    def _constant_result(
        self, level: int, lo: Optional[int], hi: Optional[int], at: Optional[int]
    ) -> PeakResult:
        """范围内没有变化点时的结果（可能有区间跨过整个范围）。"""
        if level == 0 and (lo is None or hi is None):
            return PeakResult(0, (), lo, hi)
        evidence = (
            tuple(sorted(self._intervals.active_at_or_before(at)))
            if at is not None else ()
        )
        return PeakResult(level, (PeakPeriod(lo, hi, level, evidence),), lo, hi)

    @staticmethod
    def _append_peak_period(
        periods: List[PeakPeriod],
        start: Optional[int],
        end: Optional[int],
        level: int,
        peak: int,
        active: Set[str],
    ) -> None:
        if level != peak:
            return
        evidence = tuple(sorted(active))
        if (
            periods
            and periods[-1].end == start
            and periods[-1].level == level
            and periods[-1].evidence == evidence
        ):
            previous = periods.pop()
            start = previous.start
        periods.append(PeakPeriod(start, end, level, evidence))
