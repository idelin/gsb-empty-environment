"""锚点：本地墙钟读数与参考时间点的一次对应观测，带误差区间。

误差区间可以为非对称：[ref - minus, ref + plus]。
同一本地时间若对应不相交的参考区间，将构成冲突（由求解器以冲突核报告）。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Optional, Union

from .time_model import Duration, Instant, WallTime, to_fraction


@dataclass(frozen=True)
class Anchor:
    """一条锚点观测。

    local:   本地墙钟读数；
    ref:     参考时间点（标称值）；
    minus:   参考值下偏误差界（>=0），真值不早于 ref - minus；
    plus:    参考值上偏误差界（>=0），真值不晚于 ref + plus；
    name:    可选标识，便于冲突核/剔除报告阅读。
    """

    local: WallTime
    ref: Instant
    minus: Duration
    plus: Duration
    name: str = ""

    @property
    def x(self) -> Fraction:
        """本地时间（秒，有理数）。"""
        return self.local.seconds

    @property
    def y_low(self) -> Fraction:
        """参考真值下界（秒，有理数）。"""
        return self.ref.seconds - self.minus.seconds

    @property
    def y_high(self) -> Fraction:
        """参考真值上界（秒，有理数）。"""
        return self.ref.seconds + self.plus.seconds

    def label(self) -> str:
        return self.name or f"@{self.local.exact()}"

    def describe(self) -> str:
        return (
            f"{self.label()}: local={self.local.exact()} "
            f"ref∈[{Instant(self.y_low).exact()}, {Instant(self.y_high).exact()}]"
        )


def make_anchor(
    local: Union[int, str, Fraction],
    ref: Union[int, str, Fraction],
    error: Union[int, str, Fraction] = 0,
    *,
    minus: Optional[Union[int, str, Fraction]] = None,
    plus: Optional[Union[int, str, Fraction]] = None,
    name: str = "",
) -> Anchor:
    """便捷构造：对称误差用 error，非对称误差用 minus/plus。"""
    if minus is None:
        minus = error
    if plus is None:
        plus = error
    minus_f = to_fraction(minus)
    plus_f = to_fraction(plus)
    if minus_f < 0 or plus_f < 0:
        raise ValueError("误差界必须非负")
    return Anchor(
        local=WallTime(local),
        ref=Instant(ref),
        minus=Duration(minus_f),
        plus=Duration(plus_f),
        name=name,
    )
