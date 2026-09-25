"""时间模型：区分墙钟时间、合成时间点、持续时长，全部精确表示。

- ``TimePoint``：合成时间轴上的时间点，偏移量用 ``Fraction`` 精确表示。
- ``WallClock``：墙钟时间，带时区的 ``datetime``。
- ``Duration``：持续时长，以秒为单位的 ``Fraction``，运算不丢精度。
- ``OffsetTable``：合成偏移 <-> 墙钟 的分段线性转换表（锚点精确，
  锚点之间线性插值，表外沿最近段斜率外延）。

三种类型之间不做隐式转换：``TimePoint`` 只能与 ``TimePoint`` 比较，
``WallClock`` 只能与 ``WallClock`` 比较，跨类型运算抛出 ``TypeError``。
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from fractions import Fraction
from functools import total_ordering
from typing import Iterable, Tuple, Union

Number = Union[int, Fraction]

US_PER_SECOND = 1_000_000
_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def _to_fraction(value: Union[Number, float, str]) -> Fraction:
    if isinstance(value, Fraction):
        return value
    if isinstance(value, float):
        raise TypeError("请用 int/Fraction/str 表示精确数值，避免 float 精度损失")
    return Fraction(value)


def _datetime_to_us(at: datetime) -> int:
    """带时区 datetime -> 距 Unix 纪元的微秒数（整数，精确）。"""
    delta = at.astimezone(timezone.utc) - _EPOCH
    return (
        delta.days * 86_400 * US_PER_SECOND
        + delta.seconds * US_PER_SECOND
        + delta.microseconds
    )


def _us_to_datetime(us: Union[int, Fraction]) -> datetime:
    """微秒数 -> 带时区 datetime；非整数微秒四舍五入到最近微秒。"""
    rounded = int(us) if isinstance(us, int) else round(us)
    return _EPOCH + timedelta(microseconds=rounded)


@total_ordering
@dataclass(frozen=True)
class Duration:
    """持续时长：精确的秒数（``Fraction``）。"""

    seconds: Fraction

    def __init__(self, seconds: Union[Number, str] = 0):
        object.__setattr__(self, "seconds", _to_fraction(seconds))

    @classmethod
    def from_microseconds(cls, us: Number) -> "Duration":
        return cls(Fraction(us, US_PER_SECOND))

    @property
    def microseconds(self) -> Fraction:
        """精确微秒数（``Fraction``，不舍入）。"""
        return self.seconds * US_PER_SECOND

    def _check(self, other: object) -> "Duration":
        if not isinstance(other, Duration):
            raise TypeError(f"Duration 不能与 {type(other).__name__} 混合运算")
        return other

    def __add__(self, other: object) -> "Duration":
        other = self._check(other)
        return Duration(self.seconds + other.seconds)

    def __sub__(self, other: object) -> "Duration":
        other = self._check(other)
        return Duration(self.seconds - other.seconds)

    def __mul__(self, factor: Number) -> "Duration":
        return Duration(self.seconds * _to_fraction(factor))

    __rmul__ = __mul__

    def __truediv__(self, divisor: Number) -> "Duration":
        return Duration(self.seconds / _to_fraction(divisor))

    def __neg__(self) -> "Duration":
        return Duration(-self.seconds)

    def __abs__(self) -> "Duration":
        return Duration(abs(self.seconds))

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Duration) and self.seconds == other.seconds

    def __lt__(self, other: object) -> bool:
        other = self._check(other)
        return self.seconds < other.seconds

    def __hash__(self) -> int:
        return hash(self.seconds)

    def __repr__(self) -> str:
        return f"Duration({self.seconds}s)"


@total_ordering
@dataclass(frozen=True)
class TimePoint:
    """合成时间轴上的时间点：精确偏移量（tick，``Fraction``）。"""

    offset: Fraction

    def __init__(self, offset: Union[Number, str] = 0):
        object.__setattr__(self, "offset", _to_fraction(offset))

    def __add__(self, delta: Number) -> "TimePoint":
        if isinstance(delta, (int, Fraction)):
            return TimePoint(self.offset + delta)
        return NotImplemented

    __radd__ = __add__

    def __sub__(self, other: object) -> Union["TimePoint", Fraction]:
        if isinstance(other, TimePoint):
            return self.offset - other.offset  # tick 差，精确 Fraction
        if isinstance(other, (int, Fraction)):
            return TimePoint(self.offset - other)
        return NotImplemented

    def __eq__(self, other: object) -> bool:
        return isinstance(other, TimePoint) and self.offset == other.offset

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, TimePoint):
            raise TypeError("TimePoint 只能与 TimePoint 比较（墙钟请先经 OffsetTable 转换）")
        return self.offset < other.offset

    def __hash__(self) -> int:
        return hash(self.offset)

    def __repr__(self) -> str:
        return f"TimePoint({self.offset})"


@total_ordering
@dataclass(frozen=True)
class WallClock:
    """墙钟时间：带时区的日历时间（微秒精度）。"""

    at: datetime

    def __init__(self, at: datetime):
        if not isinstance(at, datetime):
            raise TypeError("WallClock 需要 datetime")
        if at.tzinfo is None:
            raise ValueError("WallClock 需要带时区的 datetime（墙钟时间必须有时区）")
        object.__setattr__(self, "at", at)

    def __add__(self, duration: "Duration") -> "WallClock":
        if isinstance(duration, Duration):
            return WallClock(_us_to_datetime(_datetime_to_us(self.at) + duration.microseconds))
        return NotImplemented

    def __sub__(self, other: object) -> Union["WallClock", "Duration"]:
        if isinstance(other, WallClock):
            return Duration.from_microseconds(
                _datetime_to_us(self.at) - _datetime_to_us(other.at)
            )
        if isinstance(other, Duration):
            return self + (-other)
        return NotImplemented

    def __eq__(self, other: object) -> bool:
        return isinstance(other, WallClock) and self.at == other.at

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, WallClock):
            raise TypeError("WallClock 只能与 WallClock 比较（合成时间点请先经 OffsetTable 转换）")
        return self.at < other.at

    def __hash__(self) -> int:
        return hash(self.at)

    def __repr__(self) -> str:
        return f"WallClock({self.at.isoformat()})"


class OffsetTable:
    """合成偏移 <-> 墙钟 的分段线性转换表。

    锚点为 ``(偏移 tick, WallClock)`` 对，按偏移升序；锚点处精确，
    锚点之间线性插值（``Fraction`` 运算，微秒级精确），
    表外沿最近一段的斜率外延。
    """

    def __init__(self, anchors: Iterable[Tuple[Union[Number, str], WallClock]]):
        points = sorted(
            ((TimePoint(o).offset, self._as_wallclock(w)) for o, w in anchors),
            key=lambda p: p[0],
        )
        if len(points) < 2:
            raise ValueError("OffsetTable 至少需要 2 个锚点")
        offsets = [p[0] for p in points]
        walls = [_datetime_to_us(p[1].at) for p in points]
        if len(set(offsets)) != len(offsets):
            raise ValueError("锚点偏移必须互不相同")
        if any(b <= a for a, b in zip(walls, walls[1:])):
            raise ValueError("锚点墙钟必须严格递增（合成轴不允许倒流）")
        self._offsets = offsets
        self._walls_us = walls

    @staticmethod
    def _as_wallclock(value: Union[WallClock, datetime]) -> WallClock:
        return value if isinstance(value, WallClock) else WallClock(value)

    def _segment_index(self, offset: Fraction) -> int:
        for i in range(len(self._offsets) - 1):
            if self._offsets[i] <= offset <= self._offsets[i + 1]:
                return i
        return 0 if offset < self._offsets[0] else len(self._offsets) - 2

    def wallclock_us_exact(self, point: TimePoint) -> Fraction:
        """合成时间点 -> 距 Unix 纪元的微秒数（``Fraction``，精确不舍入）。"""
        i = self._segment_index(point.offset)
        o0, o1 = self._offsets[i], self._offsets[i + 1]
        w0, w1 = self._walls_us[i], self._walls_us[i + 1]
        return w0 + (point.offset - o0) * Fraction(w1 - w0, o1 - o0)

    def wallclock_at(self, point: TimePoint) -> WallClock:
        """合成时间点 -> 墙钟时间（舍入到最近微秒）。"""
        return WallClock(_us_to_datetime(self.wallclock_us_exact(point)))

    def timepoint_at(self, wall: Union[WallClock, datetime]) -> TimePoint:
        """墙钟时间 -> 合成时间点（精确 ``Fraction`` 偏移）。"""
        target = _datetime_to_us(self._as_wallclock(wall).at)
        for i in range(len(self._walls_us) - 1):
            if self._walls_us[i] <= target <= self._walls_us[i + 1]:
                break
        else:
            i = 0 if target < self._walls_us[0] else len(self._walls_us) - 2
        o0, o1 = self._offsets[i], self._offsets[i + 1]
        w0, w1 = self._walls_us[i], self._walls_us[i + 1]
        return TimePoint(o0 + Fraction(target - w0, 1) * (o1 - o0) / (w1 - w0))

    def duration_between(self, start: TimePoint, end: TimePoint) -> Duration:
        """两个合成时间点之间的墙钟持续时长（精确）。"""
        return Duration.from_microseconds(
            self.wallclock_us_exact(end) - self.wallclock_us_exact(start)
        )

    def advance(self, point: TimePoint, duration: Duration) -> TimePoint:
        """把合成时间点按墙钟时长推进，返回新的合成时间点。"""
        target_us = self.wallclock_us_exact(point) + duration.microseconds
        return self.timepoint_at(WallClock(_us_to_datetime(round(target_us))))

    def __repr__(self) -> str:
        return f"OffsetTable(anchors={len(self._offsets)})"
