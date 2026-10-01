"""分层抽样与两阶段分层成组(整群)抽样估计库。

仅使用 Python 标准库。设计原则:
- 各层总体量 N_h 与样本量 n_h 已知,层权 W_h = N_h / N,权重之和规范为 1;
- 缺层(N_h > 0 但 n_h = 0)与零纳入概率一律显式拒绝(抛异常);
- 单样本层(n_h = 1 且 N_h > 1)不假装有精确方差:均值照算,方差标记为不可估;
- 全量抽样层(n_h == N_h)方差贡献为 0;
- 两阶段成组抽样以"群"为最终分析单元,不把组内相关观测当独立样本;
- 随机源由调用方注入,seed 与样本量被记录;
- 精确(枚举)结论与经验(模拟)结论分别标注。
"""

from .stratified import (
    MissingStratumError,
    SamplingDesignError,
    StratifiedEstimate,
    StratumSample,
    VarianceDiagnostic,
    estimate_stratified_mean,
)
from .two_stage import (
    ClusterSample,
    TwoStageEstimate,
    TwoStageStratum,
    ZeroInclusionProbabilityError,
    estimate_two_stage_mean,
)
from .incremental import IncrementalStratifiedEstimator
from .sampling import SamplingRecord, draw_stratified_sample, draw_two_stage_sample
from .enumeration import (
    ExactEnumerationResult,
    enumerate_stratified,
    enumerate_two_stage,
)

__all__ = [
    "MissingStratumError",
    "SamplingDesignError",
    "StratifiedEstimate",
    "StratumSample",
    "VarianceDiagnostic",
    "estimate_stratified_mean",
    "ClusterSample",
    "TwoStageEstimate",
    "TwoStageStratum",
    "ZeroInclusionProbabilityError",
    "estimate_two_stage_mean",
    "IncrementalStratifiedEstimator",
    "SamplingRecord",
    "draw_stratified_sample",
    "draw_two_stage_sample",
    "ExactEnumerationResult",
    "enumerate_stratified",
    "enumerate_two_stage",
]
