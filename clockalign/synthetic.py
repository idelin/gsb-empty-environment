"""合成偏移转换表：确定性生成一套带真值的演示/测试数据。

场景：一只本地时钟相对参考时钟有分段不同的漂移率，
两个连续交界；观测带 ±0.05s 均匀误差区间，并注入 2 个异常锚点。
所有数值用有理数精确表示。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction

from .anchors import Anchor, make_anchor
from .model import PiecewiseModel, make_model

# 真实分段参数（仅用于生成数据与校验，求解器看不到）。
# beta 选取保证两个交界严格连续：
#   交界 1000: 1.0002*1000+50 = 0.9997*1000+50.5 = 1050.2
#   交界 3000: 0.9997*3000+50.5 = 1.0001*3000+49.3 = 3049.6
TRUE_RATES = (Fraction(10002, 10000), Fraction(9997, 10000), Fraction(10001, 10000))
TRUE_BETAS = (Fraction(50), Fraction(101, 2), Fraction(493, 10))
BREAKPOINTS = (Fraction(1000), Fraction(3000))
ANCHOR_ERROR = Fraction(1, 20)  # ±0.05s
MAX_DRIFT = Fraction(1, 1000)  # 声明的最大漂移斜率 ±0.001
EXTRAPOLATION_MARGIN = Fraction(200)

# 本地观测点
ANCHOR_LOCALS = (
    0, 200, 400, 600, 800, 950, 1100, 1500, 2000, 2500, 2900, 3100, 3500, 4000,
)
# 注入的异常：(本地时间, 注入偏移秒) —— 偏移远超误差区间
OUTLIER_SPECS = (
    (600, Fraction(3)),
    (2500, Fraction(-3)),
)


def true_reference(x: Fraction) -> Fraction:
    """真实映射（分段仿射、交界连续）。"""
    if x <= BREAKPOINTS[0]:
        return TRUE_RATES[0] * x + TRUE_BETAS[0]
    if x <= BREAKPOINTS[1]:
        return TRUE_RATES[1] * x + TRUE_BETAS[1]
    return TRUE_RATES[2] * x + TRUE_BETAS[2]


@dataclass(frozen=True)
class SyntheticTable:
    """合成偏移转换表：锚点、模型声明与真值。"""

    anchors: tuple[Anchor, ...]
    model: PiecewiseModel
    outlier_indices: tuple[int, ...]
    extrapolation_margin: Fraction

    def true_reference_at(self, x: Fraction) -> Fraction:
        return true_reference(x)


def make_synthetic_table() -> SyntheticTable:
    """生成合成偏移转换表（确定性，无随机数）。"""
    anchors: list[Anchor] = []
    for i, x in enumerate(ANCHOR_LOCALS):
        y = true_reference(Fraction(x))
        anchors.append(make_anchor(x, y, ANCHOR_ERROR, name=f"A{i:02d}"))
    outlier_indices: list[int] = []
    for j, (x, shift) in enumerate(OUTLIER_SPECS):
        y = true_reference(Fraction(x)) + shift
        outlier_indices.append(len(anchors))
        anchors.append(make_anchor(x, y, ANCHOR_ERROR, name=f"X{j:02d}(异常)"))

    model = make_model(
        BREAKPOINTS,
        rate=(1 - MAX_DRIFT, 1 + MAX_DRIFT),
        continuous=True,
    )
    return SyntheticTable(
        anchors=tuple(anchors),
        model=model,
        outlier_indices=tuple(outlier_indices),
        extrapolation_margin=EXTRAPOLATION_MARGIN,
    )
