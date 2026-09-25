"""原始约束：可更正的闭区间时间约束与显式因果边。"""

from __future__ import annotations

from dataclasses import dataclass, replace


@dataclass(frozen=True)
class IntervalConstraint:
    """事件 event 的发生时刻 t 落在闭整数区间 [low, high] 内。"""

    cid: str
    event: str
    low: int
    high: int

    def __post_init__(self) -> None:
        if not isinstance(self.low, int) or not isinstance(self.high, int):
            raise TypeError("区间端点必须是整数 tick")
        if self.low > self.high:
            raise ValueError(
                f"[{self.cid}] 空区间 {self.low}>{self.high} 本身就是矛盾"
            )

    def corrected(self, low: int | None = None, high: int | None = None):
        """返回一条更正后的新约束（修正即替换原始证据）。"""
        new_low = self.low if low is None else low
        new_high = self.high if high is None else high
        return replace(self, low=new_low, high=new_high)

    @property
    def evidence(self) -> str:
        return (
            f"[{self.cid}] 区间约束 {self.event} ∈ [{self.low}, {self.high}]"
            "（闭区间，整数 tick）"
        )


@dataclass(frozen=True)
class CausalEdge:
    """显式因果边：earlier 必然先于 later，且至少间隔 min_gap 个 tick。"""

    cid: str
    earlier: str
    later: str
    min_gap: int = 1

    def __post_init__(self) -> None:
        if self.earlier == self.later:
            raise ValueError(f"[{self.cid}] 因果边不能自环：{self.earlier}")
        if not isinstance(self.min_gap, int) or self.min_gap < 1:
            raise ValueError("因果边要求严格先后，min_gap 必须是 >=1 的整数")

    @property
    def evidence(self) -> str:
        return (
            f"[{self.cid}] 因果边 {self.earlier} ≺ {self.later}"
            f"（t_{self.later} - t_{self.earlier} ≥ {self.min_gap}）"
        )
