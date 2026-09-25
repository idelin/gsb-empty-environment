"""clockalign: 分段仿射时间校正（本地时间 -> 参考时间）。

仅使用 Python 3.11 标准库。核心能力：

- 精确有理数时间表示（区分墙钟时间 WallTime、时间点 Instant、时长 Duration）；
- 带误差区间的锚点（本地/参考时间对）；
- 在锚点误差区间与最大漂移斜率约束下求分段仿射可行映射；
- 异常剔除以"最少删锚点"为目标（精确搜索 + 冲突核 + 最优性证据），
  预算未决与已删/已保留分开记录；
- 查询上下界由所有可行映射推出（精确有理单纯形法 LP），
  并提供有理约束枚举（穷举顶点）作对照；
- 分段交界连续性按声明显式建模（连续 / 声明跳变）；
- 外推超过允许距离拒绝；同一本地时间不同参考值产生冲突核。
"""

from .time_model import Duration, Instant, WallTime
from .anchors import Anchor, make_anchor
from .model import Junction, PiecewiseModel
from .reconcile import (
    Correction,
    ExtrapolationError,
    JunctionSideError,
    Reconciliation,
    Scenario,
    correct,
    reconcile,
)

__all__ = [
    "Anchor",
    "Correction",
    "Duration",
    "ExtrapolationError",
    "Instant",
    "Junction",
    "JunctionSideError",
    "PiecewiseModel",
    "Reconciliation",
    "Scenario",
    "WallTime",
    "correct",
    "make_anchor",
    "reconcile",
]

__version__ = "0.1.0"
