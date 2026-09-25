"""分段仿射模型的声明结构。

每段形如  y = alpha_i * x + beta_i

- x 为本地墙钟秒数，y 为参考时间点秒数（均为有理数）；
- alpha_i 是漂移斜率（无量纲，接近 1），受声明的最大漂移斜率约束；
- 分段交界按 ``Junction`` 声明：
    continuous=True  显式连续性 alpha_left*b+beta_left = alpha_right*b+beta_right；
    continuous=False 允许声明跳变，跳变量受 max_jump 限制（None 表示不限制）。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Optional, Sequence, Union

from .time_model import Duration, WallTime, to_fraction


@dataclass(frozen=True)
class Junction:
    """两段之间的交界声明。"""

    breakpoint: WallTime
    continuous: bool = True
    max_jump: Optional[Duration] = None

    def __post_init__(self):
        if not self.continuous and self.max_jump is not None:
            if self.max_jump.seconds < 0:
                raise ValueError("max_jump 必须非负")


@dataclass(frozen=True)
class PiecewiseModel:
    """分段仿射模型声明。

    boundaries: 升序交界点（个数 = 段数 - 1）；
    rate_min/rate_max: 每段斜率（漂移率）的闭区间，标量则各段相同；
    junctions: 每个交界的连续性声明，标量则各交界相同。
    """

    boundaries: tuple[WallTime, ...]
    rate_min: tuple[Fraction, ...]
    rate_max: tuple[Fraction, ...]
    junctions: tuple[Junction, ...]

    @property
    def n_segments(self) -> int:
        return len(self.boundaries) + 1

    @property
    def breakpoints(self) -> tuple[Fraction, ...]:
        return tuple(b.seconds for b in self.boundaries)

    def segment_of(
        self,
        x: Fraction,
        *,
        side: str = "left",
    ) -> int:
        """返回 x 所在段的下标（0 基）。

        交界点默认归左侧段（与连续性声明一致）。
        若交界声明为不连续，调用方必须显式指定 side="left"/"right"，
        否则由调用处抛出 JunctionSideError。
        """
        for i, bp in enumerate(self.breakpoints):
            if x < bp:
                return i
            if x == bp and not self.junctions[i].continuous:
                if side == "left":
                    return i
                if side == "right":
                    return i + 1
                raise ValueError(
                    f"本地时间 {x} 位于声明不连续的交界点，"
                    "必须用 side='left' 或 side='right' 指明取哪一侧"
                )
        return self.n_segments - 1

def make_model(
    boundaries: Sequence[Union[int, str, Fraction]],
    *,
    rate: Union[
        Union[int, str, Fraction],
        tuple[Union[int, str, Fraction], Union[int, str, Fraction]],
        Sequence[tuple[Union[int, str, Fraction], Union[int, str, Fraction]]],
    ] = (1, 1),
    continuous: Union[bool, Sequence[bool]] = True,
    max_jump: Union[
        None,
        Union[int, str, Fraction],
        Sequence[Optional[Union[int, str, Fraction]]],
    ] = None,
) -> PiecewiseModel:
    """便捷构造分段模型。

    rate: 标量表示允许 [1-rate, 1+rate]；(lo, hi) 表示统一区间；
          列表则逐段指定。
    continuous: 标量 bool 应用于全部交界，否则逐交界给出。
    max_jump: 仅在该交界 continuous=False 时有效。
    """
    bps = tuple(WallTime(b) for b in boundaries)
    if any(bps[i].seconds >= bps[i + 1].seconds for i in range(len(bps) - 1)):
        raise ValueError("交界点必须严格升序")
    n = len(bps) + 1

    if isinstance(rate, tuple) and len(rate) == 2 and not isinstance(rate[0], (list, tuple)):
        lo, hi = to_fraction(rate[0]), to_fraction(rate[1])
        mins = (lo,) * n
        maxs = (hi,) * n
    elif isinstance(rate, (int, str, Fraction)):
        d = to_fraction(rate)
        mins = (1 - d,) * n
        maxs = (1 + d,) * n
    else:
        pairs = list(rate)
        if len(pairs) != n:
            raise ValueError("逐段 rate 区间个数必须等于段数")
        mins = tuple(to_fraction(p[0]) for p in pairs)
        maxs = tuple(to_fraction(p[1]) for p in pairs)
        if any(lo > hi for lo, hi in zip(mins, maxs)):
            raise ValueError("rate 下界不能大于上界")

    if isinstance(continuous, bool):
        cont_flags = [continuous] * (n - 1)
    else:
        cont_flags = list(continuous)
        if len(cont_flags) != n - 1:
            raise ValueError("continuous 个数必须等于交界数")

    if max_jump is None or isinstance(max_jump, (int, str, Fraction)):
        jumps = [None if max_jump is None else to_fraction(max_jump)] * (n - 1)
    else:
        jumps = [None if j is None else to_fraction(j) for j in max_jump]
        if len(jumps) != n - 1:
            raise ValueError("max_jump 个数必须等于交界数")

    junctions = tuple(
        Junction(
            breakpoint=bps[i],
            continuous=bool(cont_flags[i]),
            max_jump=None if cont_flags[i] else (None if jumps[i] is None else Duration(jumps[i])),
        )
        for i in range(n - 1)
    )
    return PiecewiseModel(
        boundaries=bps,
        rate_min=mins,
        rate_max=maxs,
        junctions=junctions,
    )
