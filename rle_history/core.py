"""带来源索引的设备状态历史运行长度编码（RLE）。

模型
----
* 事件 ``Event``：在某个整数时刻对设备状态的一次观测，携带 ``(source, seq)`` 身份。
* 段 ``Segment``：连续相同状态合并成的半开区间 ``[start, end)``，
  ``end is None`` 表示延伸到 +∞。段内保留形成它的全部事件身份
  （``sources``），以及同时刻冲突中被覆盖的事件身份（``overridden``）。
* 补丁 ``SegmentPatch``：一次编辑（插入 / 撤回 / 更正）引起的段级增量，
  由 ``removed`` 与 ``added`` 两组段组成，可施加在旧段列表上得到新段列表。

冲突规则
--------
同一时刻出现多条事件时，按 ``(seq, source)`` 字典序取**最后一条**生效；
其余事件不丢弃，作为 ``overridden`` 证据挂在覆盖其时刻的段上。
撤回生效事件后，被覆盖的事件会自然重新生效。
"""

from __future__ import annotations

from bisect import bisect_left, bisect_right, insort
from collections import Counter
from dataclasses import dataclass
from itertools import groupby
from typing import Callable, Iterable, Optional

__all__ = [
    "Event",
    "EventRef",
    "Segment",
    "SegmentPatch",
    "StateHistory",
    "apply_patch",
    "build_segments",
]


# ---------------------------------------------------------------------------
# 值对象
# ---------------------------------------------------------------------------


@dataclass(frozen=True, order=True)
class EventRef:
    """事件身份：来源 + 来源内序号。"""

    source: str
    seq: int

    def __str__(self) -> str:  # 便于日志与断言阅读
        return f"{self.source}#{self.seq}"


@dataclass(frozen=True)
class Event:
    """一条原始观测记录。"""

    time: int
    state: str
    source: str
    seq: int

    @property
    def ref(self) -> EventRef:
        return EventRef(self.source, self.seq)


def _event_order(event: Event) -> tuple[int, int, str]:
    """事件的全序：先按时刻，同刻按 (seq, source)，最后者生效。"""
    return (event.time, event.seq, event.source)


@dataclass(frozen=True)
class Segment:
    """一段状态恒定的半开区间 [start, end)，end 为 None 表示 +∞。"""

    state: str
    start: int
    end: Optional[int]
    #: 形成本段的事件身份（含中间重复记录），按 (time, seq, source) 排序。
    sources: tuple[EventRef, ...] = ()
    #: 落在本段区间内、但因同刻冲突被覆盖的事件身份。
    overridden: tuple[EventRef, ...] = ()

    def covers(self, time: int) -> bool:
        return self.start <= time and (self.end is None or time < self.end)

    @property
    def is_open(self) -> bool:
        return self.end is None


@dataclass(frozen=True)
class SegmentPatch:
    """段级增量补丁：先移除 removed，再加入 added。"""

    removed: tuple[Segment, ...] = ()
    added: tuple[Segment, ...] = ()

    @property
    def is_empty(self) -> bool:
        return not self.removed and not self.added

    def __bool__(self) -> bool:
        return not self.is_empty


def _segment_key(seg: Segment) -> tuple[int, float]:
    return (seg.start, float("inf") if seg.end is None else seg.end)


def apply_patch(
    segments: Iterable[Segment], patch: SegmentPatch
) -> list[Segment]:
    """把补丁施加到段列表上，返回新的有序段列表。

    按值匹配移除（允许重复段各移除一次）；若补丁引用了列表中
    不存在的段，说明消费方与生产方已发散，抛出 ``ValueError``。
    """
    remaining: Counter[Segment] = Counter(patch.removed)
    out: list[Segment] = []
    for seg in segments:
        if remaining.get(seg, 0) > 0:
            remaining[seg] -= 1
            continue
        out.append(seg)
    if any(remaining.values()):
        missing = [str(s) for s, n in remaining.items() if n > 0]
        raise ValueError(f"patch does not apply, missing segments: {missing}")
    out.extend(patch.added)
    out.sort(key=_segment_key)
    return out


# ---------------------------------------------------------------------------
# 段构建（参考实现：由原始事件全量重算）
# ---------------------------------------------------------------------------


def _build_runs(events: list[Event], end: Optional[int] = None) -> list[Segment]:
    """把按 _event_order 排序的事件折叠成段，最后一段在 ``end`` 处封口。"""
    runs: list[Segment] = []
    cur_state: Optional[str] = None
    run_start = 0
    sources: list[EventRef] = []
    overridden: list[EventRef] = []
    for time, group in groupby(events, key=lambda e: e.time):
        members = list(group)  # 组内已按 (seq, source) 排序
        winner = members[-1]
        if cur_state is None or winner.state != cur_state:
            if cur_state is not None:
                runs.append(
                    Segment(cur_state, run_start, time, tuple(sources), tuple(overridden))
                )
            cur_state = winner.state
            run_start = time
            sources = []
            overridden = []
        for event in members:
            target = sources if event.state == cur_state else overridden
            target.append(event.ref)
    if cur_state is not None:
        runs.append(
            Segment(cur_state, run_start, end, tuple(sources), tuple(overridden))
        )
    return runs


def build_segments(events: Iterable[Event]) -> list[Segment]:
    """参考实现：直接从原始事件重算全部段（用于核对增量路径）。"""
    return _build_runs(sorted(events, key=_event_order))


def _diff(
    old: list[Segment], new: list[Segment]
) -> tuple[list[Segment], list[Segment]]:
    """去掉共同前后缀，得到最小化的 (removed, added)。"""
    head = 0
    while head < len(old) and head < len(new) and old[head] == new[head]:
        head += 1
    tail = 0
    while (
        tail < len(old) - head
        and tail < len(new) - head
        and old[len(old) - 1 - tail] == new[len(new) - 1 - tail]
    ):
        tail += 1
    old_end = len(old) - tail if tail else len(old)
    new_end = len(new) - tail if tail else len(new)
    return old[head:old_end], new[head:new_end]


# ---------------------------------------------------------------------------
# 主结构
# ---------------------------------------------------------------------------


class StateHistory:
    """可编辑的设备状态历史：查询当前段，编辑返回增量补丁。

    编辑操作（``insert`` / ``retract`` / ``correct``）只重算受影响
    时间窗内的段，返回最小化的 ``SegmentPatch``；窗口外的段保持
    对象不变，补丁消费方可以安全地做增量物化。
    """

    def __init__(self, events: Iterable[Event] = ()) -> None:
        self._events: dict[EventRef, Event] = {}
        self._sorted: list[Event] = []          # 按 _event_order 有序
        self._time_counts: Counter[int] = Counter()
        self._times: list[int] = []             # 有序去重时刻表
        self._segments: list[Segment] = []
        self._starts: list[int] = []            # 段起点，用于二分
        initial = list(events)
        refs = [e.ref for e in initial]
        if len(set(refs)) != len(refs):
            raise ValueError("duplicate (source, seq) in initial events")
        for event in initial:
            self._store(event)
        self._segments = _build_runs(self._sorted)
        self._starts = [s.start for s in self._segments]

    # -- 查询 ------------------------------------------------------------

    def segments(self) -> list[Segment]:
        """当前全部段，按起点升序、首尾相接。"""
        return list(self._segments)

    def events(self) -> list[Event]:
        """当前全部存活事件，按 (time, seq, source) 排序。"""
        return list(self._sorted)

    def segment_at(self, time: int) -> Optional[Segment]:
        """覆盖时刻 ``time`` 的段；该时刻无有效状态时返回 None。"""
        if not self._starts:
            return None
        index = bisect_right(self._starts, time) - 1
        if index < 0:
            return None
        seg = self._segments[index]
        return seg if seg.covers(time) else None

    def state_at(self, time: int) -> Optional[str]:
        """时刻 ``time`` 的有效状态。"""
        seg = self.segment_at(time)
        return seg.state if seg is not None else None

    def provenance(self, time: int) -> Optional[tuple[EventRef, ...]]:
        """时刻 ``time`` 所在段的来源事件身份（含被合并的重复记录）。"""
        seg = self.segment_at(time)
        return None if seg is None else seg.sources

    def __len__(self) -> int:
        return len(self._events)

    def __contains__(self, ref: EventRef) -> bool:
        return ref in self._events

    # -- 编辑（均返回增量补丁） ------------------------------------------

    def insert(self, time: int, state: str, source: str, seq: int) -> SegmentPatch:
        """插入一条事件；(source, seq) 已存在时抛 ``ValueError``。"""
        ref = EventRef(source, seq)
        if ref in self._events:
            raise ValueError(f"duplicate event {ref}")
        event = Event(int(time), state, source, int(seq))
        return self._mutate([event.time], lambda: self._store(event))

    def retract(self, source: str, seq: int) -> SegmentPatch:
        """撤回一条事件；不存在时抛 ``KeyError``。"""
        ref = EventRef(source, seq)
        event = self._events.get(ref)
        if event is None:
            raise KeyError(f"unknown event {ref}")
        return self._mutate([event.time], lambda: self._unstore(event))

    def correct(
        self,
        source: str,
        seq: int,
        *,
        state: Optional[str] = None,
        time: Optional[int] = None,
    ) -> SegmentPatch:
        """更正一条事件的状态和/或时刻，作为一次原子编辑输出单个补丁。"""
        ref = EventRef(source, seq)
        old = self._events.get(ref)
        if old is None:
            raise KeyError(f"unknown event {ref}")
        new = Event(
            old.time if time is None else int(time),
            old.state if state is None else state,
            source,
            seq,
        )
        if new == old:
            return SegmentPatch()

        def mutate() -> None:
            self._unstore(old)
            self._store(new)

        return self._mutate([old.time, new.time], mutate)

    # -- 内部：事件存储 ----------------------------------------------------

    def _store(self, event: Event) -> None:
        self._events[event.ref] = event
        insort(self._sorted, event, key=_event_order)
        if self._time_counts[event.time] == 0:
            insort(self._times, event.time)
        self._time_counts[event.time] += 1

    def _unstore(self, event: Event) -> None:
        del self._events[event.ref]
        index = bisect_left(self._sorted, _event_order(event), key=_event_order)
        assert self._sorted[index].ref == event.ref
        self._sorted.pop(index)
        self._time_counts[event.time] -= 1
        if self._time_counts[event.time] == 0:
            del self._time_counts[event.time]
            self._times.pop(bisect_left(self._times, event.time))

    # -- 内部：增量重算 ----------------------------------------------------

    def _segment_covering(self, time: int) -> Segment:
        index = bisect_right(self._starts, time) - 1
        return self._segments[index]

    def _point_window(self, time: int) -> tuple[Optional[int], Optional[int]]:
        """单个受影响时刻的重算窗口 [lo, hi)，None 表示无界。

        窗口取「前一有效事件所在段的起点」到「后一有效事件所在段的终点」，
        可证明窗口外的段在编辑前后完全一致。
        """
        lo: Optional[int] = None
        hi: Optional[int] = None
        before = bisect_left(self._times, time)
        if before > 0:
            lo = self._segment_covering(self._times[before - 1]).start
        after = bisect_right(self._times, time)
        if after < len(self._times):
            hi = self._segment_covering(self._times[after]).end
        return lo, hi

    def _window(self, times: list[int]) -> tuple[Optional[int], Optional[int]]:
        points = [self._point_window(t) for t in times]
        los = [p[0] for p in points if p[0] is not None]
        his = [p[1] for p in points if p[1] is not None]
        lo = None if len(los) < len(points) else min(los)
        hi = None if len(his) < len(points) else max(his)
        return lo, hi

    def _mutate(self, times: list[int], mutate: Callable[[], None]) -> SegmentPatch:
        lo, hi = self._window(times)  # 基于编辑前结构计算，窗口外两侧版本一致
        old = self._segments
        a = 0 if lo is None else bisect_left(self._starts, lo)
        b = len(old) if hi is None else bisect_left(self._starts, hi)
        mutate()
        window_events = [
            e
            for e in self._sorted
            if (lo is None or e.time >= lo) and (hi is None or e.time < hi)
        ]
        new_runs = _build_runs(window_events, end=hi)
        removed, added = _diff(old[a:b], new_runs)
        self._segments = old[:a] + new_runs + old[b:]
        self._starts = [s.start for s in self._segments]
        return SegmentPatch(tuple(removed), tuple(added))
