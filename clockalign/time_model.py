"""精确时间模型：区分墙钟时间、时间点与持续时长，全部用有理数表示。

三类时间不可混用：

- ``WallTime``：本地墙钟读数（如 "10:00:00.5"），只有相对意义，
  两个 WallTime 相减得到本地时长，可加/减 Duration 平移；
- ``Instant``：参考时间轴上的绝对时间点，相对参考纪元（epoch）的偏移，
  两个 Instant 相减得到参考时长，可加/减 Duration 平移；
- ``Duration``：时长（可为负），支持加减、乘除有理数、比较。

内部一律以秒的有理数（``fractions.Fraction``）存储，保证"时间精确表示"：
所有计算无浮点误差。字符串输出同时给出精确值与十进制近似。
"""

from __future__ import annotations

from fractions import Fraction
from typing import Union

Number = Union[int, Fraction]


def to_fraction(value: Union[int, float, str, Fraction]) -> Fraction:
    """把 int / Fraction / 十进制字符串转成精确有理数。

    拒绝 float，避免隐式二进制浮点误差进入系统。
    """
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool 不是合法的时间数值")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"不支持的时间数值类型: {type(value)!r}（请用 int/Fraction/十进制字符串）")


def format_seconds(value: Fraction) -> str:
    """精确格式：整数或分数。"""
    if value.denominator == 1:
        return str(value.numerator)
    return f"{value.numerator}/{value.denominator}"


def approx_seconds(value: Fraction, places: int = 6) -> str:
    """十进制近似格式（仅用于展示）。"""
    scaled = float(value)
    return f"{scaled:.{places}f}"


class Duration:
    """持续时长（秒的有理数，可为负）。"""

    __slots__ = ("_seconds",)

    def __init__(self, seconds: Union[int, str, Fraction] = 0):
        self._seconds = to_fraction(seconds)

    @classmethod
    def of(cls, seconds: Union[int, str, Fraction]) -> "Duration":
        return cls(seconds)

    @property
    def seconds(self) -> Fraction:
        return self._seconds

    def __add__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds + other._seconds)
        return NotImplemented

    def __sub__(self, other):
        if isinstance(other, Duration):
            return Duration(self._seconds - other._seconds)
        return NotImplemented

    def __neg__(self):
        return Duration(-self._seconds)

    def __mul__(self, factor):
        if isinstance(factor, (int, Fraction)):
            return Duration(self._seconds * factor)
        return NotImplemented

    __rmul__ = __mul__

    def __truediv__(self, divisor):
        if isinstance(divisor, (int, Fraction)):
            return Duration(self._seconds / divisor)
        if isinstance(divisor, Duration):
            return self._seconds / divisor._seconds  # 无量纲比值
        return NotImplemented

    def _cmp_key(self):
        return self._seconds

    def __eq__(self, other):
        return isinstance(other, Duration) and self._seconds == other._seconds

    def __lt__(self, other):
        if isinstance(other, Duration):
            return self._seconds < other._seconds
        return NotImplemented

    def __le__(self, other):
        if isinstance(other, Duration):
            return self._seconds <= other._seconds
        return NotImplemented

    def __gt__(self, other):
        if isinstance(other, Duration):
            return self._seconds > other._seconds
        return NotImplemented

    def __ge__(self, other):
        if isinstance(other, Duration):
            return self._seconds >= other._seconds
        return NotImplemented

    def __hash__(self):
        return hash((Duration, self._seconds))

    def exact(self) -> str:
        return f"{format_seconds(self._seconds)}s"

    def approx(self, places: int = 6) -> str:
        return f"{approx_seconds(self._seconds, places)}s"

    def __repr__(self):
        return f"Duration({self.exact()})"


class WallTime:
    """本地墙钟读数。只有相对意义：可相减得时长、可加减时长平移。"""

    __slots__ = ("_seconds", "label")

    def __init__(self, seconds: Union[int, str, Fraction] = 0, label: str = ""):
        self._seconds = to_fraction(seconds)
        self.label = label

    @classmethod
    def of(cls, seconds: Union[int, str, Fraction], label: str = "") -> "WallTime":
        return cls(seconds, label)

    @property
    def seconds(self) -> Fraction:
        return self._seconds

    def __sub__(self, other):
        if isinstance(other, WallTime):
            return Duration(self._seconds - other._seconds)
        if isinstance(other, Duration):
            return WallTime(self._seconds - other._seconds, self.label)
        return NotImplemented

    def __add__(self, other):
        if isinstance(other, Duration):
            return WallTime(self._seconds + other._seconds, self.label)
        return NotImplemented

    def __eq__(self, other):
        return isinstance(other, WallTime) and self._seconds == other._seconds

    def __lt__(self, other):
        if isinstance(other, WallTime):
            return self._seconds < other._seconds
        return NotImplemented

    def __le__(self, other):
        if isinstance(other, WallTime):
            return self._seconds <= other._seconds
        return NotImplemented

    def __gt__(self, other):
        if isinstance(other, WallTime):
            return self._seconds > other._seconds
        return NotImplemented

    def __ge__(self, other):
        if isinstance(other, WallTime):
            return self._seconds >= other._seconds
        return NotImplemented

    def __hash__(self):
        return hash((WallTime, self._seconds))

    def exact(self) -> str:
        return f"T+{format_seconds(self._seconds)}s"

    def approx(self, places: int = 6) -> str:
        return f"T+{approx_seconds(self._seconds, places)}s"

    def __repr__(self):
        tag = f" '{self.label}'" if self.label else ""
        return f"WallTime({self.exact()}{tag})"


class Instant:
    """参考时间轴上的绝对时间点（相对参考纪元的偏移）。"""

    __slots__ = ("_seconds",)

    def __init__(self, seconds: Union[int, str, Fraction] = 0):
        self._seconds = to_fraction(seconds)

    @classmethod
    def of(cls, seconds: Union[int, str, Fraction]) -> "Instant":
        return cls(seconds)

    @property
    def seconds(self) -> Fraction:
        return self._seconds

    def __sub__(self, other):
        if isinstance(other, Instant):
            return Duration(self._seconds - other._seconds)
        if isinstance(other, Duration):
            return Instant(self._seconds - other._seconds)
        return NotImplemented

    def __add__(self, other):
        if isinstance(other, Duration):
            return Instant(self._seconds + other._seconds)
        return NotImplemented

    def __eq__(self, other):
        return isinstance(other, Instant) and self._seconds == other._seconds

    def __lt__(self, other):
        if isinstance(other, Instant):
            return self._seconds < other._seconds
        return NotImplemented

    def __le__(self, other):
        if isinstance(other, Instant):
            return self._seconds <= other._seconds
        return NotImplemented

    def __gt__(self, other):
        if isinstance(other, Instant):
            return self._seconds > other._seconds
        return NotImplemented

    def __ge__(self, other):
        if isinstance(other, Instant):
            return self._seconds >= other._seconds
        return NotImplemented

    def __hash__(self):
        return hash((Instant, self._seconds))

    def exact(self) -> str:
        return f"R+{format_seconds(self._seconds)}s"

    def approx(self, places: int = 6) -> str:
        return f"R+{approx_seconds(self._seconds, places)}s"

    def __repr__(self):
        return f"Instant({self.exact()})"
