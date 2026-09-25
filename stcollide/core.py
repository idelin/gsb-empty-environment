"""精确碰撞检测核心数学。

所有坐标、速度、时刻均为 int 或 fractions.Fraction（有理数），
全部比较与求根都是精确运算，不使用任何 epsilon 容差。

拓扑约定（显式规则）：
- 盒为闭集：两盒相交（含边界相贴）即视为碰撞。
- 对每个碰撞区间进一步区分：
  - "overlap"：存在某一时刻两盒内部（严格不等式）相交；
  - "contact"：持续接触但内部始终不相交（如沿棱滑移）；
  - "touch"：仅在单一时刻相切（零时长区间）。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


def F(value):
    """把 int/Fraction 规范化为 Fraction，拒绝其它类型（保证精确性）。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, int):
        return Fraction(value)
    raise TypeError(f"坐标必须为 int 或 Fraction，得到 {type(value).__name__}")


def vec(values):
    return tuple(F(v) for v in values)


def le_interval(c, v, t0, t1):
    """[t0,t1] 内满足 c + v*(t - t0) <= 0 的闭区间 (lo, hi)，无解返回 None。"""
    if v == 0:
        return (t0, t1) if c <= 0 else None
    root = t0 - c / v
    if v > 0:
        lo, hi = t0, min(t1, root)
    else:
        lo, hi = max(t0, root), t1
    return (lo, hi) if lo <= hi else None


def lt_interval(c, v, t0, t1):
    """[t0,t1] 内满足 c + v*(t - t0) < 0 的开区间 (lo, hi)，无解返回 None。"""
    if v == 0:
        if c < 0 and t0 < t1:
            return (t0, t1)
        return None
    root = t0 - c / v
    if v > 0:
        lo, hi = t0, min(t1, root)
    else:
        lo, hi = max(t0, root), t1
    return (lo, hi) if lo < hi else None


@dataclass(frozen=True)
class PieceHit:
    """单个匀速分段内的碰撞结果。

    [ta, tb] 为闭集相交区间；(sa, sb) 为内部严格相交的开区间，
    无内部相交时 sa/sb 为 None。
    """

    ta: Fraction
    tb: Fraction
    sa: Fraction | None
    sb: Fraction | None

    @property
    def has_interior(self):
        return self.sa is not None


def piece_collision(pos_a, size_a, vel_a, pos_b, size_b, vel_b, s, e):
    """两盒在分段 [s,e] 内（各自匀速）的碰撞，返回 PieceHit 或 None。

    pos_* 为 s 时刻盒的最小角坐标，size_* 为各轴边长，vel_* 为该段速度。
    """
    closed_lo, closed_hi = s, e
    open_lo, open_hi = s, e
    open_ok = True
    for axis in range(len(pos_a)):
        a_lo = pos_a[axis]
        a_hi = a_lo + size_a[axis]
        b_lo = pos_b[axis]
        b_hi = b_lo + size_b[axis]
        va = vel_a[axis]
        vb = vel_b[axis]
        for c, v in ((a_lo - b_hi, va - vb), (b_lo - a_hi, vb - va)):
            iv = le_interval(c, v, s, e)
            if iv is None:
                return None
            closed_lo = max(closed_lo, iv[0])
            closed_hi = min(closed_hi, iv[1])
            if closed_lo > closed_hi:
                return None
            ov = lt_interval(c, v, s, e)
            if ov is None:
                open_ok = False
            else:
                open_lo = max(open_lo, ov[0])
                open_hi = min(open_hi, ov[1])
    if open_ok and open_lo < open_hi:
        return PieceHit(closed_lo, closed_hi, open_lo, open_hi)
    return PieceHit(closed_lo, closed_hi, None, None)
