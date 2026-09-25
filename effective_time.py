"""有效时长计算库。

严格区分三种时间概念:
- WallTime  墙钟时间:本地钟表显示的时刻,不含偏移信息。
- Instant   时间点:绝对时间轴上的位置(自纪元起的微秒数),与偏移无关。
- Duration  持续时长:两个时间点之间的真实经过时间,精确到微秒。

偏移转换由合成转换表 OffsetTable 描述(前拨/回拨均为合成数据),
跨偏移转换的时长一律按真实持续时间计算。
"""

from __future__ import annotations

from bisect import bisect_right
from dataclasses import dataclass
from datetime import date, datetime, time, timedelta
from typing import Iterable

_EPOCH = datetime(2000, 1, 1)
_MICROS_PER_DAY = 86_400_000_000


def _datetime_to_micros(value: datetime) -> int:
    delta = value - _EPOCH
    return delta.days * _MICROS_PER_DAY + delta.seconds * 1_000_000 + delta.microseconds


def _micros_to_datetime(micros: int) -> datetime:
    return _EPOCH + timedelta(microseconds=micros)


def _timedelta_to_micros(delta: timedelta) -> int:
    return delta.days * _MICROS_PER_DAY + delta.seconds * 1_000_000 + delta.microseconds


def _require(value, expected_type, name):
    if not isinstance(value, expected_type):
        raise TypeError(
            f"{name} 需要 {expected_type.__name__},实际为 {type(value).__name__}"
        )


@dataclass(frozen=True, order=True)
class Duration:
    """持续时长:真实经过的时间,精确到微秒。"""

    micros: int

    @classmethod
    def of(cls, *, hours=0.0, minutes=0.0, seconds=0.0, milliseconds=0.0, microseconds=0) -> "Duration":
        delta = timedelta(
            hours=hours, minutes=minutes, seconds=seconds,
            milliseconds=milliseconds, microseconds=microseconds,
        )
        return cls(_timedelta_to_micros(delta))

    @classmethod
    def from_timedelta(cls, delta: timedelta) -> "Duration":
        return cls(_timedelta_to_micros(delta))

    def to_timedelta(self) -> timedelta:
        return timedelta(microseconds=self.micros)

    def total_seconds(self) -> float:
        return self.micros / 1_000_000

    def __add__(self, other):
        _require(other, Duration, "Duration + ?")
        return Duration(self.micros + other.micros)

    def __sub__(self, other):
        _require(other, Duration, "Duration - ?")
        return Duration(self.micros - other.micros)

    def __neg__(self):
        return Duration(-self.micros)

    def __repr__(self):
        return f"Duration(micros={self.micros})"


@dataclass(frozen=True, order=True)
class Instant:
    """时间点:绝对时间轴上的位置,与偏移无关。"""

    micros: int

    def __add__(self, other):
        _require(other, Duration, "Instant + ?")
        return Instant(self.micros + other.micros)

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Instant(self.micros - other.micros)
        if isinstance(other, Instant):
            return Duration(self.micros - other.micros)
        raise TypeError(f"Instant - {type(other).__name__} 不受支持")

    def __repr__(self):
        return f"Instant(micros={self.micros})"


@dataclass(frozen=True, order=True)
class WallTime:
    """墙钟时间:naive 本地时刻,不含偏移信息。"""

    value: datetime

    def __post_init__(self):
        if not isinstance(self.value, datetime):
            raise TypeError("WallTime.value 必须是 datetime")
        if self.value.tzinfo is not None:
            raise ValueError("WallTime 必须是 naive datetime(不含 tzinfo)")

    def __repr__(self):
        return f"WallTime({self.value.isoformat()})"


@dataclass(frozen=True)
class OffsetTransition:
    """一次偏移跳变。wall_time 按跳变前偏移解读;跳变后墙钟直接跳到新偏移。"""

    wall_time: WallTime
    offset_before: timedelta
    offset_after: timedelta


class OffsetTable:
    """合成偏移转换表:base_offset 为初始偏移,transitions 按墙钟时间排序。"""

    def __init__(self, base_offset: timedelta, transitions: Iterable[OffsetTransition] = ()):
        ordered = sorted(transitions, key=lambda t: t.wall_time.value)
        self._offsets = [_timedelta_to_micros(base_offset)]
        self._starts: list[int] = []  # 每个转换发生的真实时刻(Instant.micros)
        previous = None
        for transition in ordered:
            before = _timedelta_to_micros(transition.offset_before)
            after = _timedelta_to_micros(transition.offset_after)
            if before != self._offsets[-1]:
                raise ValueError("offset_before 必须等于前一段的偏移")
            start = _datetime_to_micros(transition.wall_time.value) - before
            if previous is not None and start <= previous:
                raise ValueError("偏移转换的真实时刻必须严格递增")
            self._starts.append(start)
            self._offsets.append(after)
            previous = start

    def instant_to_wall(self, instant: Instant) -> WallTime:
        _require(instant, Instant, "instant_to_wall")
        index = bisect_right(self._starts, instant.micros)
        return WallTime(_micros_to_datetime(instant.micros + self._offsets[index]))

    def wall_to_instant(self, wall: WallTime, *, ambiguous: str = "first") -> Instant:
        """墙钟转时间点。

        回拨产生的重叠墙钟由 ambiguous 决定取 "first" 或 "last" 出现;
        前拨产生的空洞墙钟一律顺延到空洞结束(即跳变后的第一个有效时刻)。
        """
        _require(wall, WallTime, "wall_to_instant")
        if ambiguous not in ("first", "last"):
            raise ValueError("ambiguous 必须是 'first' 或 'last'")
        target = _datetime_to_micros(wall.value)
        matches = []
        count = len(self._offsets)
        for index in range(count):
            offset = self._offsets[index]
            lo_inst = self._starts[index - 1] if index > 0 else None
            hi_inst = self._starts[index] if index < count - 1 else None
            if lo_inst is not None and target < lo_inst + offset:
                continue
            if hi_inst is not None and target >= hi_inst + offset:
                continue
            matches.append(target - offset)
        if matches:
            return Instant(matches[0] if ambiguous == "first" else matches[-1])
        # 空洞:顺延到空洞结束
        for index in range(1, count):
            lo_inst = self._starts[index - 1]
            if target < lo_inst + self._offsets[index]:
                return Instant(lo_inst)
        raise AssertionError("不可达:墙钟时间既无匹配也不在任何空洞中")


@dataclass(frozen=True)
class DailyWindow:
    """每日开放段(墙钟时间,当日 start < end)。"""

    start: time
    end: time


@dataclass(frozen=True)
class ClosedInterval:
    """例外关闭段(墙钟时间,半开区间 [start, end))。"""

    start: WallTime
    end: WallTime


def _merge(intervals: list[tuple[int, int]]) -> list[tuple[int, int]]:
    merged: list[tuple[int, int]] = []
    for lo, hi in sorted(intervals):
        if hi <= lo:
            continue
        if merged and lo <= merged[-1][1]:
            merged[-1] = (merged[-1][0], max(merged[-1][1], hi))
        else:
            merged.append((lo, hi))
    return merged


def _subtract(intervals, closed):
    result = []
    for lo, hi in intervals:
        cursor = lo
        for c_lo, c_hi in closed:
            if c_hi <= cursor:
                continue
            if c_lo >= hi:
                break
            if c_lo > cursor:
                result.append((cursor, min(c_lo, hi)))
            cursor = max(cursor, c_hi)
            if cursor >= hi:
                break
        if cursor < hi:
            result.append((cursor, hi))
    return result


class EffectiveCalendar:
    """由每日开放段与例外关闭段构成的日历,基于偏移转换表按真实时长计算。"""

    def __init__(
        self,
        table: OffsetTable,
        windows: Iterable[DailyWindow],
        closed: Iterable[ClosedInterval] = (),
    ):
        self._table = table
        self._windows = list(windows)
        if not self._windows:
            raise ValueError("至少需要一个每日开放段")
        for window in self._windows:
            if not window.start < window.end:
                raise ValueError("每日开放段必须满足 start < end")
        raw_closed = []
        for interval in closed:
            lo = table.wall_to_instant(interval.start).micros
            hi = table.wall_to_instant(interval.end).micros
            if hi > lo:
                raw_closed.append((lo, hi))
        # 关闭段先合并,重叠部分只扣一次
        self._closed = _merge(raw_closed)

    def _open_for_day(self, day: date) -> list[tuple[int, int]]:
        raw = []
        for window in self._windows:
            lo = self._table.wall_to_instant(WallTime(datetime.combine(day, window.start))).micros
            hi = self._table.wall_to_instant(WallTime(datetime.combine(day, window.end))).micros
            if hi > lo:
                raw.append((lo, hi))
        return _subtract(_merge(raw), self._closed)

    def open_intervals_between(self, start: Instant, end: Instant) -> list[tuple[Instant, Instant]]:
        """[start, end) 内的有效开放区间(已扣除关闭段并按真实时间轴表示)。"""
        _require(start, Instant, "start")
        _require(end, Instant, "end")
        if end.micros < start.micros:
            raise ValueError("end 不得早于 start")
        first_day = self._table.instant_to_wall(start).value.date() - timedelta(days=1)
        last_day = self._table.instant_to_wall(end).value.date() + timedelta(days=1)
        result = []
        day = first_day
        while day <= last_day:
            for lo, hi in self._open_for_day(day):
                clipped_lo = max(lo, start.micros)
                clipped_hi = min(hi, end.micros)
                if clipped_hi > clipped_lo:
                    result.append((Instant(clipped_lo), Instant(clipped_hi)))
            day += timedelta(days=1)
        return result

    def effective_duration(self, start: Instant, end: Instant) -> Duration:
        """两时点之间的有效时长(真实持续时间)。"""
        total = sum(
            hi.micros - lo.micros
            for lo, hi in self.open_intervals_between(start, end)
        )
        return Duration(total)

    def add(self, start: Instant, duration: Duration) -> Instant:
        """从起点加有效时长反求终点。

        沿开放区间累积真实时长;若终点落在关闭段(含开放段结束边界),
        跳到下一开放段起点。
        """
        _require(start, Instant, "start")
        _require(duration, Duration, "duration")
        if duration.micros < 0:
            raise ValueError("duration 不得为负")
        remaining = duration.micros
        day = self._table.instant_to_wall(start).value.date() - timedelta(days=1)
        while True:
            for lo, hi in self._open_for_day(day):
                if hi <= start.micros:
                    continue
                cursor = max(lo, start.micros)
                available = hi - cursor
                if remaining < available:
                    return Instant(cursor + remaining)
                remaining -= available
                if remaining == 0:
                    # 恰好落在开放段结束边界(即关闭段起点),跳到下一开放段
                    start = Instant(hi)
            day += timedelta(days=1)
