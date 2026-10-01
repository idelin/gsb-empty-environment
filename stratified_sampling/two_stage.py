"""两阶段分层成组(整群)抽样:均值估计与方差可估性诊断。

设计:总体分为若干层;层 h 内有 M_h 个群、共 N_h 个单元。
- 第一阶段:层内抽取 m_h 个群,群 i 的纳入概率为 pi_i;
- 第二阶段:入样群 i 内抽取单元,单元条件纳入概率为 pi_{j|i}(层内等概)。

点估计(Horvitz-Thompson 型,同时考虑两阶段纳入概率):
    z_i   = (1/pi_i) * sum_{j in s_i} y_ij / pi_{j|i}   # 入样群的加权群总量
    t_h   = sum_{i in s_h} z_i                            # 层总量估计
    mean  = sum_h t_h / N,  N = sum_h N_h                 # 总体均值估计

方差(最终群 ultimate cluster 法,含有限总体修正):
    v_h = m_h * (1 - m_h/M_h) * s_z^2,  v(mean) = sum_h v_h / N^2
  其中 s_z^2 为层内加权群总量 z_i 的样本方差。注意 z_i 已含 1/pi_i
  权重,故 Var(t_h) = Var(sum_{i in s} z_i) = m_h(1-f_h)S_z^2,
  不能再套未加权群总量的 M_h^2(1-f_h)S_T^2/m_h 形式(会重复计入
  (M_h/m_h)^2)。一阶段等概不放回时上式精确;不等概时为最终群近似。
  组内相关观测绝不当作独立样本:方差只在"群"这一层聚合,
  单元数不进入自由度。

规则:
- 零(或负、越界)纳入概率显式拒绝,抛 ZeroInclusionProbabilityError;
- 缺层(层存在但无入样群)显式拒绝,抛 MissingStratumError;
- 层内仅 1 个入样群且未全查(m_h = 1 < M_h):方差不可估,给诊断,不伪造;
- 群被全查的层(m_h == M_h)方差贡献为 0。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from math import sqrt
from statistics import fmean

from .stratified import MissingStratumError, SamplingDesignError, VarianceDiagnostic


class ZeroInclusionProbabilityError(SamplingDesignError):
    """纳入概率为零或越界,显式拒绝。"""


def _check_prob(value: float, what: str) -> None:
    if not (0.0 < value <= 1.0):
        raise ZeroInclusionProbabilityError(
            f"{what} 的纳入概率必须落在 (0, 1],得到 {value}"
        )


@dataclass(frozen=True)
class ClusterSample:
    """一个入样群。

    name:                   群名
    pop_cluster_size:       群内总体单元数 M_i(已知)
    inclusion_prob:         一阶段纳入概率 pi_i
    element_inclusion_prob: 二阶段单元条件纳入概率 pi_{j|i}(等概)
    values:                 入样单元观测值
    """

    name: str
    pop_cluster_size: int
    inclusion_prob: float
    element_inclusion_prob: float
    values: tuple[float, ...]

    def __post_init__(self) -> None:
        if self.pop_cluster_size < 1:
            raise SamplingDesignError(
                f"群 {self.name!r} 的总体单元数必须 >= 1,得到 {self.pop_cluster_size}"
            )
        _check_prob(self.inclusion_prob, f"群 {self.name!r} 一阶段")
        _check_prob(self.element_inclusion_prob, f"群 {self.name!r} 二阶段")
        if len(self.values) == 0:
            raise SamplingDesignError(f"群 {self.name!r} 没有入样单元")
        if len(self.values) > self.pop_cluster_size:
            raise SamplingDesignError(
                f"群 {self.name!r} 入样单元数超过群内总体单元数"
            )

    @property
    def weighted_total(self) -> float:
        """该群对层总量的加权贡献 z_i(同时含两阶段纳入概率)。"""
        within = sum(y / self.element_inclusion_prob for y in self.values)
        return within / self.inclusion_prob


@dataclass(frozen=True)
class TwoStageStratum:
    """两阶段设计中的一个层。

    pop_clusters: 层内群总数 M_h;pop_elements: 层内单元总数 N_h。
    """

    name: str
    pop_clusters: int
    pop_elements: int
    clusters: tuple[ClusterSample, ...]

    def __post_init__(self) -> None:
        if self.pop_clusters < 1 or self.pop_elements < 1:
            raise SamplingDesignError(
                f"层 {self.name!r} 的群总数与单元总数都必须 >= 1"
            )
        m = len(self.clusters)
        if m == 0:
            raise MissingStratumError(
                f"层 {self.name!r} 在总体中存在(M_h={self.pop_clusters} 个群)"
                "但无入样群,拒绝估计"
            )
        if m > self.pop_clusters:
            raise SamplingDesignError(
                f"层 {self.name!r} 入样群数 m_h={m} 超过层内群总数 M_h={self.pop_clusters}"
            )


@dataclass(frozen=True)
class TwoStageEstimate:
    """两阶段分层成组抽样估计结果。"""

    mean: float
    variance: float | None
    standard_error: float | None
    variance_estimable: bool
    stratum_cluster_counts: dict[str, int]
    pop_cluster_counts: dict[str, int]
    pop_sizes: dict[str, int]
    diagnostics: tuple[VarianceDiagnostic, ...] = field(default_factory=tuple)


def estimate_two_stage_mean(strata: list[TwoStageStratum]) -> TwoStageEstimate:
    """两阶段分层成组抽样的均值估计与方差可估性诊断。"""
    if not strata:
        raise SamplingDesignError("至少需要一个层")
    names = [s.name for s in strata]
    if len(set(names)) != len(names):
        raise SamplingDesignError("层名重复")

    total_pop = sum(s.pop_elements for s in strata)

    total_hat = 0.0
    variance_of_total = 0.0
    estimable = True
    diagnostics: list[VarianceDiagnostic] = []

    for s in strata:
        z_values = [c.weighted_total for c in s.clusters]
        total_hat += sum(z_values)

        m_h = len(s.clusters)
        big_m = s.pop_clusters
        if m_h == big_m:
            # 层内群全查:有限总体修正因子为 0,方差贡献为 0。
            continue
        if m_h == 1:
            estimable = False
            diagnostics.append(
                VarianceDiagnostic(
                    stratum=s.name,
                    reason=(
                        f"层内仅 1 个入样群(m_h=1, M_h={big_m}):"
                        "群间方差不可估,整体方差标记为不可估"
                    ),
                )
            )
            continue
        z_bar = fmean(z_values)
        s2_z = sum((z - z_bar) ** 2 for z in z_values) / (m_h - 1)
        fpc = 1.0 - m_h / big_m  # 一阶段有限总体修正
        variance_of_total += m_h * fpc * s2_z

    mean = total_hat / total_pop
    variance = variance_of_total / (total_pop * total_pop)

    return TwoStageEstimate(
        mean=mean,
        variance=variance if estimable else None,
        standard_error=sqrt(variance) if estimable else None,
        variance_estimable=estimable,
        stratum_cluster_counts={s.name: len(s.clusters) for s in strata},
        pop_cluster_counts={s.name: s.pop_clusters for s in strata},
        pop_sizes={s.name: s.pop_elements for s in strata},
        diagnostics=tuple(diagnostics),
    )
