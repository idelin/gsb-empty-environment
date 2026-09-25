"""优先级区间叠加时间轴。

把若干带优先级的半开区间 ``[start, end)`` 条目叠成一条不重叠的
分段时间轴：

- 每个位置上只有最高优先级的条目生效；
- 同一位置出现多个并列最高优先级条目时，该段标记为冲突段，
  所有候选（值 + 来源）都显式保留，不静默取舍；
- 支持局部更新：增删条目只重算受影响的窗口，窗口外的分段原样保留；
- 来源跟踪：每个条目带来源标识，分段可回溯到贡献它的条目与来源；
- 相邻分段只有在“胜出条目集合”完全一致时才合并，因此相邻同值但
  不同来源的证据不会被误丢。
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from fractions import Fraction
from typing import Any, Dict, Iterator, List, Optional, Tuple, Union

from .time_model import TimePoint


class ConflictError(ValueError):
    """在同级冲突段上请求单一值时抛出。"""


@dataclass(frozen=True)
class Entry:
    """一条原始区间记录（证据），由 ``Timeline.add`` 创建。"""

    id: int
    start: TimePoint
    end: TimePoint
    value: Any
    priority: int
    source: str

    def __post_init__(self) -> None:
        if not self.start.offset < self.end.offset:
            raise ValueError(f"区间必须满足 start < end，得到 [{self.start}, {self.end})")


@dataclass(frozen=True)
class Segment:
    """解析后的不重叠分段。

    ``entries`` 是贡献该段的原始条目（证据）：同级冲突段保留全部并列
    候选；同值同来源的相邻段合并后，各自条目也都保留在 ``entries`` 中。
    ``is_conflict`` 标识该段是否存在同级并列（同一时刻多个最高优先级
    条目同时生效）。
    """

    start: TimePoint
    end: TimePoint
    priority: int
    entries: Tuple[Entry, ...]
    is_conflict: bool = False

    @property
    def value(self) -> Any:
        if self.is_conflict:
            raise ConflictError(
                f"同级冲突段 [{self.start}, {self.end}) 有 {len(self.entries)} 个候选，"
                "请使用 .values / .entries"
            )
        return self.entries[0].value

    @property
    def values(self) -> Tuple[Any, ...]:
        return tuple(e.value for e in self.entries)

    @property
    def sources(self) -> Tuple[str, ...]:
        """该段的来源标识（去重，按条目 id 顺序）。"""
        return tuple(dict.fromkeys(e.source for e in self.entries))

    def clipped(self, lo: Fraction, hi: Fraction) -> "Segment":
        return Segment(TimePoint(lo), TimePoint(hi), self.priority, self.entries,
                       self.is_conflict)


class Timeline:
    """带优先级的区间值叠加时间轴（局部更新 + 来源跟踪）。"""

    def __init__(self) -> None:
        self._entries: Dict[int, Entry] = {}
        self._segments: List[Segment] = []  # 不重叠、按起点升序
        self._next_id = 0

    # ---------------------------------------------------------------- 增删

    def add(
        self,
        start: Union[TimePoint, int, Fraction],
        end: Union[TimePoint, int, Fraction],
        value: Any,
        *,
        priority: int = 0,
        source: str = "",
    ) -> int:
        """新增条目，返回条目 id；只重算与该区间重叠的窗口。"""
        start = start if isinstance(start, TimePoint) else TimePoint(start)
        end = end if isinstance(end, TimePoint) else TimePoint(end)
        entry = Entry(self._next_id, start, end, value, priority, source)
        self._next_id += 1
        self._entries[entry.id] = entry
        self._refresh(entry.start.offset, entry.end.offset)
        return entry.id

    def remove(self, entry_id: int) -> Entry:
        """按 id 撤回条目；只重算该条目覆盖的窗口，底层条目自动恢复。"""
        try:
            entry = self._entries.pop(entry_id)
        except KeyError:
            raise KeyError(f"条目不存在: {entry_id}") from None
        self._refresh(entry.start.offset, entry.end.offset)
        return entry

    def withdraw_source(self, source: str) -> List[int]:
        """撤回某来源的全部条目，返回被撤回的条目 id 列表。"""
        doomed = [e for e in self._entries.values() if e.source == source]
        for entry in doomed:
            del self._entries[entry.id]
        for entry in doomed:  # 逐窗口局部重算
            self._refresh(entry.start.offset, entry.end.offset)
        return [e.id for e in doomed]

    # ---------------------------------------------------------------- 查询

    def entries(self, source: Optional[str] = None) -> List[Entry]:
        """原始条目（证据），可按来源过滤。"""
        items = sorted(self._entries.values(), key=lambda e: e.id)
        if source is not None:
            items = [e for e in items if e.source == source]
        return items

    def segments(
        self,
        start: Optional[Union[TimePoint, int, Fraction]] = None,
        end: Optional[Union[TimePoint, int, Fraction]] = None,
    ) -> List[Segment]:
        """查询分段；给定范围时裁剪到 ``[start, end)``。"""
        if start is None and end is None:
            return list(self._segments)
        lo = Fraction(-10**30) if start is None else (
            start.offset if isinstance(start, TimePoint) else Fraction(start)
        )
        hi = Fraction(10**30) if end is None else (
            end.offset if isinstance(end, TimePoint) else Fraction(end)
        )
        out = []
        for seg in self._segments:
            s, e = seg.start.offset, seg.end.offset
            if e <= lo or s >= hi:
                continue
            out.append(seg.clipped(max(s, lo), min(e, hi)))
        return out

    def segment_at(self, point: Union[TimePoint, int, Fraction]) -> Optional[Segment]:
        """某时间点生效的分段（含冲突段），无覆盖时返回 None。"""
        offset = point.offset if isinstance(point, TimePoint) else Fraction(point)
        starts = [s.start.offset for s in self._segments]
        i = bisect_right(starts, offset) - 1
        if i >= 0 and self._segments[i].end.offset > offset:
            return self._segments[i]
        return None

    def conflicts(self) -> List[Segment]:
        """全部同级冲突段（显式保留的并列最高优先级候选）。"""
        return [s for s in self._segments if s.is_conflict]

    def __iter__(self) -> Iterator[Segment]:
        return iter(self._segments)

    def __len__(self) -> int:
        return len(self._segments)

    # ------------------------------------------------------------ 局部重算

    def _refresh(self, lo: Fraction, hi: Fraction) -> None:
        """只重算窗口 [lo, hi) 内的分段，窗口外原样保留。"""
        resolved = self._resolve_window(lo, hi)
        kept: List[Segment] = []
        for seg in self._segments:
            s, e = seg.start.offset, seg.end.offset
            if e <= lo or s >= hi:
                kept.append(seg)  # 窗口外，原样保留
                continue
            if s < lo:  # 跨越左边界：保留窗口外部分
                kept.append(seg.clipped(s, lo))
            if e > hi:  # 跨越右边界：保留窗口外部分
                kept.append(seg.clipped(hi, e))
        kept.extend(resolved)
        kept.sort(key=lambda seg: seg.start.offset)
        self._segments = self._coalesce(kept)

    def _resolve_window(self, lo: Fraction, hi: Fraction) -> List[Segment]:
        """在窗口 [lo, hi) 内用扫描线重算最高优先级分段。"""
        covering = [
            e for e in self._entries.values()
            if e.start.offset < hi and e.end.offset > lo
        ]
        if not covering:
            return []
        points = {lo, hi}
        for e in covering:
            points.add(max(e.start.offset, lo))
            points.add(min(e.end.offset, hi))
        ordered = sorted(points)
        out: List[Segment] = []
        for a, b in zip(ordered, ordered[1:]):
            active = [
                e for e in covering
                if e.start.offset <= a and e.end.offset >= b
            ]
            if not active:
                continue
            top = max(e.priority for e in active)
            winners = tuple(sorted(
                (e for e in active if e.priority == top), key=lambda e: e.id
            ))
            out.append(Segment(TimePoint(a), TimePoint(b), top, winners,
                               len(winners) > 1))
        return self._coalesce(out)

    @staticmethod
    def _coalesce(segs: List[Segment]) -> List[Segment]:
        """合并相邻且“生效内容”一致的分段。

        合并键是 (priority, is_conflict, values, sources)：相邻同值且同
        来源的段合并为一段（双方条目都留在 entries 中，证据不丢）；
        相邻同值但来源不同的段不合并，各自来源独立保留。
        """
        out: List[Segment] = []
        for seg in segs:
            if (
                out
                and out[-1].end == seg.start
                and out[-1].priority == seg.priority
                and out[-1].is_conflict == seg.is_conflict
                and out[-1].values == seg.values
                and out[-1].sources == seg.sources
            ):
                prev = out[-1]
                merged = tuple(sorted(
                    {e.id: e for e in prev.entries + seg.entries}.values(),
                    key=lambda e: e.id,
                ))
                out[-1] = Segment(prev.start, seg.end, prev.priority, merged,
                                  prev.is_conflict)
            else:
                out.append(seg)
        return out
