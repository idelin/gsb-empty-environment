"""分段匀速（piecewise constant-velocity）轨迹，坐标与时间为 int 或 Fraction。

所有运算精确进行（fractions.Fraction），不使用任何 epsilon 容差。
轨迹在时间上连续：下一段的起点位置必须等于前一段按速度外推的位置。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def F(value):
    """把 int/Fraction 规范化为 Fraction；拒绝浮点等不精确类型。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise TypeError("bool is not a valid coordinate")
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"coordinate must be int or Fraction, got {type(value).__name__}")


def as_vector(values):
    vec = tuple(F(v) for v in values)
    if not vec:
        raise ValueError("vector must have at least one dimension")
    return vec


@dataclass(frozen=True)
class Segment:
    """从 start 起、以速度 vel 匀速运动的一段；pos 为 start 时刻的位置。"""

    start: Fraction
    pos: tuple
    vel: tuple

    def position_at(self, t: Fraction) -> tuple:
        dt = F(t) - self.start
        return tuple(p + v * dt for p, v in zip(self.pos, self.vel))


class Trajectory:
    """按 start 递增的有序段序列；最后一段延伸到 +inf。"""

    __slots__ = ("segments", "dim")

    def __init__(self, segments):
        segs = tuple(segments)
        if not segs:
            raise ValueError("trajectory needs at least one segment")
        dim = len(segs[0].pos)
        if dim == 0:
            raise ValueError("dimension must be positive")
        for seg in segs:
            if len(seg.pos) != dim or len(seg.vel) != dim:
                raise ValueError("inconsistent segment dimension")
        for prev, nxt in zip(segs, segs[1:]):
            if nxt.start <= prev.start:
                raise ValueError("segment starts must be strictly increasing")
            if prev.position_at(nxt.start) != nxt.pos:
                raise ValueError("trajectory must be continuous across segments")
        self.segments = segs
        self.dim = dim

    @classmethod
    def constant(cls, pos, vel, start=0):
        return cls([Segment(F(start), as_vector(pos), as_vector(vel))])

    @property
    def birth(self) -> Fraction:
        return self.segments[0].start

    def segment_at(self, t: Fraction) -> Segment:
        t = F(t)
        if t < self.segments[0].start:
            raise ValueError("time is before trajectory start")
        seg = self.segments[0]
        for cand in self.segments:
            if cand.start <= t:
                seg = cand
            else:
                break
        return seg

    def position_at(self, t: Fraction) -> tuple:
        return self.segment_at(t).position_at(t)
