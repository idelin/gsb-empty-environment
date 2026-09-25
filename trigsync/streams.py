"""触发流规则：版本化的相位规则、暂停区间与例外触发点。

- ``PhaseRule``     自 start 起生效的 (period, phase)，后出现的规则覆盖先前的（相位切换）
- ``PauseRule``     半开区间 [start, end)，期间该流的周期性触发被抑制
- ``ExceptionRule`` 显式例外触发点（即使落在暂停区间内也触发）

每条规则都带 ``version``，流的 digest 绑定全部规则及其版本号。
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass
from fractions import Fraction
from typing import Optional, Tuple

from .timebase import TimeValue, as_fraction


@dataclass(frozen=True)
class PhaseRule:
    version: int
    start: Fraction
    period: Fraction
    phase: Fraction

    def __init__(self, version: int, start: TimeValue, period: TimeValue, phase: TimeValue):
        object.__setattr__(self, "version", version)
        object.__setattr__(self, "start", as_fraction(start))
        object.__setattr__(self, "period", as_fraction(period))
        object.__setattr__(self, "phase", as_fraction(phase))
        if self.period <= 0:
            raise ValueError("period 必须为正")


@dataclass(frozen=True)
class PauseRule:
    version: int
    start: Fraction
    end: Fraction

    def __init__(self, version: int, start: TimeValue, end: TimeValue):
        object.__setattr__(self, "version", version)
        object.__setattr__(self, "start", as_fraction(start))
        object.__setattr__(self, "end", as_fraction(end))
        if not self.start < self.end:
            raise ValueError("暂停区间必须满足 start < end")


@dataclass(frozen=True)
class ExceptionRule:
    version: int
    at: Fraction

    def __init__(self, version: int, at: TimeValue):
        object.__setattr__(self, "version", version)
        object.__setattr__(self, "at", as_fraction(at))


@dataclass(frozen=True)
class Stream:
    """一条触发流：版本化相位序列 + 暂停区间 + 例外点。"""

    name: str
    phases: Tuple[PhaseRule, ...]
    pauses: Tuple[PauseRule, ...] = ()
    exceptions: Tuple[ExceptionRule, ...] = ()

    def __post_init__(self):
        object.__setattr__(self, "phases", tuple(sorted(self.phases, key=lambda p: (p.start, p.version))))
        object.__setattr__(self, "pauses", tuple(self.pauses))
        object.__setattr__(self, "exceptions", tuple(self.exceptions))
        if not self.phases:
            raise ValueError(f"流 {self.name!r} 至少需要一条相位规则")

    def active_phase(self, t: Fraction) -> Optional[PhaseRule]:
        """t 时刻生效的相位规则（start <= t 的最后一条）。"""
        chosen = None
        for rule in self.phases:
            if rule.start <= t:
                chosen = rule
            else:
                break
        return chosen

    def digest(self) -> str:
        """绑定全部规则及其版本号的稳定摘要。"""
        canonical = repr((self.name, self.phases, self.pauses, self.exceptions))
        return hashlib.sha256(canonical.encode("utf-8")).hexdigest()
