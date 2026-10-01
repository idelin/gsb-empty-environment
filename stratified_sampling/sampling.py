"""抽样执行:随机源由调用方注入,seed 与样本量被记录。

调用方负责构造 random.Random(seed) 并传入;本模块不自行创建随机源,
以保证可复现性与可审计性。返回的 SamplingRecord 记录 seed、各层(各群)
样本量与抽样方法,便于事后核查。
"""

from __future__ import annotations

import random
from dataclasses import dataclass, field

from .stratified import SamplingDesignError, StratumSample
from .two_stage import ClusterSample, TwoStageStratum


@dataclass(frozen=True)
class SamplingRecord:
    """一次抽样的审计记录。"""

    seed: int | None
    method: str
    sample_sizes: dict[str, int]
    detail: dict[str, dict[str, int]] = field(default_factory=dict)


def draw_stratified_sample(
    population: dict[str, list[float]],
    sample_sizes: dict[str, int],
    rng: random.Random,
    seed: int | None = None,
) -> tuple[list[StratumSample], SamplingRecord]:
    """分层简单随机不放回抽样。

    rng 由调用方注入;seed 仅作记录(调用方创建 rng 时使用的种子)。
    """
    samples: list[StratumSample] = []
    for name, values in population.items():
        n_pop = len(values)
        n = sample_sizes.get(name, 0)
        if n <= 0:
            raise SamplingDesignError(f"层 {name!r} 的抽样量必须为正(缺层拒绝)")
        if n > n_pop:
            raise SamplingDesignError(
                f"层 {name!r} 抽样量 n_h={n} 超过总体量 N_h={n_pop}"
            )
        drawn = rng.sample(values, n)
        samples.append(
            StratumSample(name=name, pop_size=n_pop, values=tuple(drawn))
        )
    record = SamplingRecord(
        seed=seed,
        method="stratified-srs-wor",
        sample_sizes={s.name: len(s.values) for s in samples},
    )
    return samples, record


def draw_two_stage_sample(
    population: dict[str, dict[str, list[float]]],
    clusters_per_stratum: dict[str, int],
    elements_per_cluster: int,
    rng: random.Random,
    seed: int | None = None,
) -> tuple[list[TwoStageStratum], SamplingRecord]:
    """两阶段分层成组抽样:层内等概抽群,群内等概抽单元。

    纳入概率由设计确定并写入结果:pi_i = m_h/M_h,pi_{j|i} = n_i/M_i。
    """
    strata: list[TwoStageStratum] = []
    detail: dict[str, dict[str, int]] = {}
    for name, clusters in population.items():
        big_m = len(clusters)
        m_h = clusters_per_stratum.get(name, 0)
        if m_h <= 0:
            raise SamplingDesignError(f"层 {name!r} 未抽任何群(缺层拒绝)")
        if m_h > big_m:
            raise SamplingDesignError(
                f"层 {name!r} 抽群数 m_h={m_h} 超过群总数 M_h={big_m}"
            )
        chosen = rng.sample(sorted(clusters), m_h)
        pop_elements = sum(len(v) for v in clusters.values())
        cluster_samples: list[ClusterSample] = []
        detail[name] = {}
        for cname in chosen:
            members = clusters[cname]
            n_i = elements_per_cluster
            if n_i > len(members):
                raise SamplingDesignError(
                    f"群 {cname!r} 抽样量 {n_i} 超过群内单元数 {len(members)}"
                )
            drawn = rng.sample(members, n_i)
            cluster_samples.append(
                ClusterSample(
                    name=cname,
                    pop_cluster_size=len(members),
                    inclusion_prob=m_h / big_m,
                    element_inclusion_prob=n_i / len(members),
                    values=tuple(drawn),
                )
            )
            detail[name][cname] = n_i
        strata.append(
            TwoStageStratum(
                name=name,
                pop_clusters=big_m,
                pop_elements=pop_elements,
                clusters=tuple(cluster_samples),
            )
        )
    record = SamplingRecord(
        seed=seed,
        method="two-stage-stratified-cluster-srs-wor",
        sample_sizes={s.name: len(s.clusters) for s in strata},
        detail=detail,
    )
    return strata, record
