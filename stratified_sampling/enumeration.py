"""有限总体全设计枚举:精确核验估计量的无偏性与方差公式。

对小总体枚举全部可能的抽样设计(每层 C(N_h, n_h) 个样本的笛卡尔积;
两阶段为群组合与单元组合的乘积),用 Fraction 精确计算:
- 估计量的期望 E[est] 与真值比较 -> 精确无偏性结论;
- 估计量的方差 Var[est] 与公式值比较 -> 精确方差核验。

所有结论标记为"精确";模拟得到的结论另处标记为"经验",两者不混淆。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from itertools import combinations, product


@dataclass(frozen=True)
class ExactEnumerationResult:
    """全设计枚举的精确核验结果。"""

    label: str  # 恒为 "精确(全设计枚举)"
    n_designs: int
    true_mean: Fraction
    expectation: Fraction
    variance: Fraction
    formula_variance: Fraction | None
    unbiased: bool
    variance_matches_formula: bool | None


def _mean_frac(values: list[Fraction]) -> Fraction:
    return sum(values, Fraction(0)) / len(values)


def _enumerate(values: list[Fraction], n: int) -> list[tuple[Fraction, ...]]:
    return list(combinations(values, n))


def enumerate_stratified(
    population: dict[str, list[float]],
    sample_sizes: dict[str, int],
) -> ExactEnumerationResult:
    """分层随机抽样:枚举全部设计,精确核验均值估计的无偏性与方差公式。"""
    names = list(population)
    pops = {k: [Fraction(str(v)) for v in population[k]] for k in names}
    total_pop = sum(len(v) for v in pops.values())
    weights = {k: Fraction(len(pops[k]), total_pop) for k in names}
    true_mean = _mean_frac([y for v in pops.values() for y in v])

    # 每层全部可能样本
    per_stratum = [_enumerate(pops[k], sample_sizes[k]) for k in names]

    estimates: list[Fraction] = []
    for combo in product(*per_stratum):
        est = sum(
            weights[k] * _mean_frac(list(sample))
            for k, sample in zip(names, combo)
        )
        estimates.append(est)

    expectation = _mean_frac(estimates)
    variance = _mean_frac([(e - expectation) ** 2 for e in estimates])

    # 公式方差:sum_h W_h^2 (1 - n_h/N_h) S_h^2 / n_h,S_h^2 用 ddof=1。
    formula = Fraction(0)
    for k in names:
        vals = pops[k]
        n_pop = len(vals)
        n = sample_sizes[k]
        mu = _mean_frac(vals)
        s2 = sum((y - mu) ** 2 for y in vals) / (n_pop - 1) if n_pop > 1 else Fraction(0)
        formula += weights[k] ** 2 * (1 - Fraction(n, n_pop)) * s2 / n

    return ExactEnumerationResult(
        label="精确(全设计枚举)",
        n_designs=len(estimates),
        true_mean=true_mean,
        expectation=expectation,
        variance=variance,
        formula_variance=formula,
        unbiased=expectation == true_mean,
        variance_matches_formula=variance == formula,
    )


def enumerate_two_stage(
    population: dict[str, dict[str, list[float]]],
    clusters_per_stratum: dict[str, int],
    elements_per_cluster: int,
) -> ExactEnumerationResult:
    """两阶段分层成组抽样:枚举全部设计,精确核验无偏性。

    当二阶段为全查(elements_per_cluster 等于每个群的单元数)时,
    同时精确核验最终群方差公式;否则 formula_variance 为 None,
    方差公式适用性需以经验(模拟)方式另行评估并标注。
    """
    names = list(population)
    pops: dict[str, dict[str, list[Fraction]]] = {
        h: {c: [Fraction(str(y)) for y in ys] for c, ys in population[h].items()}
        for h in names
    }
    total_pop = sum(len(ys) for h in names for ys in pops[h].values())
    true_mean = _mean_frac([y for h in names for ys in pops[h].values() for y in ys])

    # 每层全部可能设计:(群组合) x (各入样群的单元组合)
    per_stratum_designs: list[list[tuple]] = []
    census_second_stage = True
    for h in names:
        m_h = clusters_per_stratum[h]
        big_m = len(pops[h])
        pi_i = Fraction(m_h, big_m)
        cluster_designs: list[tuple] = []
        for cluster_combo in combinations(sorted(pops[h]), m_h):
            element_options = []
            for c in cluster_combo:
                m_i = len(pops[h][c])
                if elements_per_cluster != m_i:
                    census_second_stage = False
                element_options.append(_enumerate(pops[h][c], elements_per_cluster))
            for element_combo in product(*element_options):
                cluster_designs.append((cluster_combo, element_combo, pi_i))
        per_stratum_designs.append(cluster_designs)

    estimates: list[Fraction] = []
    for design in product(*per_stratum_designs):
        total_hat = Fraction(0)
        for h, (cluster_combo, element_combo, pi_i) in zip(names, design):
            for c, elements in zip(cluster_combo, element_combo):
                m_i = len(pops[h][c])
                pi_elem = Fraction(len(elements), m_i)
                z_i = sum(y / pi_elem for y in elements) / pi_i
                total_hat += z_i
        estimates.append(total_hat / total_pop)

    expectation = _mean_frac(estimates)
    variance = _mean_frac([(e - expectation) ** 2 for e in estimates])

    formula: Fraction | None = None
    matches: bool | None = None
    if census_second_stage:
        # 二阶段全查:z_i = T_i / pi_i 为精确群总量,最终群公式精确。
        formula = Fraction(0)
        for h in names:
            m_h = clusters_per_stratum[h]
            big_m = len(pops[h])
            pi_i = Fraction(m_h, big_m)
            z_totals = [sum(pops[h][c]) / pi_i for c in pops[h]]
            if big_m > 1:
                mu_z = _mean_frac(z_totals)
                s2_z = sum((z - mu_z) ** 2 for z in z_totals) / (big_m - 1)
            else:
                s2_z = Fraction(0)
            # z_i 已含 1/pi_i 权重:Var(t_h) = m_h(1-f_h)S_z^2。
            formula += m_h * (1 - Fraction(m_h, big_m)) * s2_z
        formula /= Fraction(total_pop) ** 2
        matches = variance == formula

    return ExactEnumerationResult(
        label="精确(全设计枚举)",
        n_designs=len(estimates),
        true_mean=true_mean,
        expectation=expectation,
        variance=variance,
        formula_variance=formula,
        unbiased=expectation == true_mean,
        variance_matches_formula=matches,
    )
