"""精确时间基元。

严格区分三种时间概念，禁止混用：

- ``Instant``   合成时间轴上的时间点（point in time）
- ``Duration``  持续时长（两个时间点之差）
- ``WallClock`` 墙钟时间（相对墙钟纪元的秒数）

所有数值一律用 ``fractions.Fraction`` 精确表示，绝不经过浮点。
``TickScale`` 把所有有理量统一到 ``1/denominator`` 的整数刻度上：
Python 整数任意精度，因此不会溢出；刻度取全体分母的最小公倍数，
因此不会丢精度。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import gcd
from typing import Iterable, Tuple, Union

TimeValue = Union[int, Fraction]


def as_fraction(value: TimeValue) -> Fraction:
    """把 int/Fraction 规范化为 Fraction，拒绝浮点等其它类型。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int) and not isinstance(value, bool):
        return Fraction(value)
    raise TypeError(f"时间值必须是 int 或 Fraction，而非 {type(value).__name__}")


@dataclass(frozen=True, order=True)
class Duration:
    """持续时长。"""

    value: Fraction

    def __init__(self, value: TimeValue):
        object.__setattr__(self, "value", as_fraction(value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value + other.value)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self.value - other.value)
        return NotImplemented

    def __mul__(self, factor):
        if isinstance(factor, int):
            return Duration(self.value * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __neg__(self):
        return Duration(-self.value)


@dataclass(frozen=True, order=True)
class Instant:
    """合成时间轴上的时间点。Instant+Instant 是类型错误。"""

    value: Fraction

    def __init__(self, value: TimeValue):
        object.__setattr__(self, "value", as_fraction(value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return Instant(self.value + other.value)
        return NotImplemented

    def __radd__(self, other):
        if isinstance(other, Duration):
            return Instant(self.value + other.value)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Instant(self.value - other.value)
        if isinstance(other, Instant):
            return Duration(self.value - other.value)
        return NotImplemented


@dataclass(frozen=True, order=True)
class WallClock:
    """墙钟时间（相对墙钟纪元的精确秒数）。"""

    value: Fraction

    def __init__(self, value: TimeValue):
        object.__setattr__(self, "value", as_fraction(value))

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallClock(self.value + other.value)
        return NotImplemented

    def __radd__(self, other):
        if isinstance(other, Duration):
            return WallClock(self.value + other.value)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return WallClock(self.value - other.value)
        if isinstance(other, WallClock):
            return Duration(self.value - other.value)
        return NotImplemented


@dataclass(frozen=True)
class OffsetEntry:
    """合成偏移转换表条目：自 instant_start 起，wall = wall_start + (t - instant_start) * rate。"""

    instant_start: Fraction
    wall_start: Fraction
    rate: Fraction  # 墙钟秒 / 合成单位，必须为正

    def __init__(self, instant_start: TimeValue, wall_start: TimeValue, rate: TimeValue = 1):
        object.__setattr__(self, "instant_start", as_fraction(instant_start))
        object.__setattr__(self, "wall_start", as_fraction(wall_start))
        object.__setattr__(self, "rate", as_fraction(rate))
        if self.rate <= 0:
            raise ValueError("rate 必须为正")


class OffsetTable:
    """合成偏移转换表：分段线性的 Instant <-> WallClock 精确映射。"""

    def __init__(self, entries: Iterable[OffsetEntry]):
        ordered = tuple(sorted(entries, key=lambda e: e.instant_start))
        if not ordered:
            raise ValueError("转换表至少需要一个条目")
        self._entries: Tuple[OffsetEntry, ...] = ordered

    @property
    def entries(self) -> Tuple[OffsetEntry, ...]:
        return self._entries

    def _entry_for_instant(self, value: Fraction) -> OffsetEntry:
        chosen = self._entries[0]
        for entry in self._entries:
            if entry.instant_start <= value:
                chosen = entry
            else:
                break
        return chosen

    def _entry_for_wall(self, value: Fraction) -> OffsetEntry:
        chosen = self._entries[0]
        for entry in self._entries:
            if entry.wall_start <= value:
                chosen = entry
            else:
                break
        return chosen

    def to_wall(self, instant: Instant) -> WallClock:
        entry = self._entry_for_instant(instant.value)
        return WallClock(entry.wall_start + (instant.value - entry.instant_start) * entry.rate)

    def to_instant(self, wall: WallClock) -> Instant:
        entry = self._entry_for_wall(wall.value)
        return Instant(entry.instant_start + (wall.value - entry.wall_start) / entry.rate)


class TickScale:
    """统一刻度：1/denominator 为一个 tick。

    denominator 取所有输入分母的最小公倍数，任何输入都能精确落到整数
    tick 上；tick 用 Python int 表示，任意精度，不溢出、不丢精度。
    """

    def __init__(self, denominators: Iterable[int]):
        unit = 1
        for den in denominators:
            if den <= 0:
                raise ValueError("分母必须为正")
            unit = unit * den // gcd(unit, den)
        self.denominator = unit

    def to_ticks(self, value: TimeValue) -> int:
        scaled = as_fraction(value) * self.denominator
        if scaled.denominator != 1:
            raise ValueError(f"{scaled} 无法被刻度 1/{self.denominator} 精确表示")
        return scaled.numerator

    def to_time(self, ticks: int) -> Fraction:
        return Fraction(ticks, self.denominator)
