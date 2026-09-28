"""蒙特卡洛经验估计（与精确结论分开报告）。

每轮独立注入新的 SeededBernoulli，记录 seed 列表；结果标注 kind='empirical'，
并给出比例的标准误，便于和精确递推结果对照。
"""
from __future__ import annotations

import math
import random
from dataclasses import dataclass

from .config import SprtConfig
from .evidence import Decision, Evidence
from .runner import run_once


@dataclass(frozen=True)
class EmpiricalReport:
    kind: str
    true_p: float
    replications: int
    accept_h1: float
    accept_h0: float
    inconclusive: float
    mean_n: float
    se_accept_h1: float
    se_accept_h0: float
    se_inconclusive: float
    se_mean_n: float
    base_seed: object
    seeds: tuple


def _se_prop(p: float, r: int) -> float:
    return math.sqrt(max(p * (1.0 - p), 0.0) / r) if r else float("nan")


def simulate(config: SprtConfig, true_p: float, replications: int = 10000,
             *, base_seed=None, sample_budget: int | None = None) -> EmpiricalReport:
    if replications <= 0:
        raise ValueError("replications 必须为正")
    rng = random.Random(base_seed)
    counts = {Decision.ACCEPT_H1: 0, Decision.ACCEPT_H0: 0, Decision.INCONCLUSIVE: 0}
    interrupted = 0
    sum_n = 0
    sum_n2 = 0
    seeds = []
    for _ in range(replications):
        seed = rng.random()
        seeds.append(seed)
        sampler = _FastBernoulli(true_p, seed)
        rec = run_once(config, sampler, Evidence.fresh(config), sample_budget=sample_budget)
        if rec.interrupted:
            interrupted += 1
        else:
            counts[rec.decision] += 1
        sum_n += rec.n
        sum_n2 += rec.n * rec.n
    r = replications
    p1 = counts[Decision.ACCEPT_H1] / r
    p0 = counts[Decision.ACCEPT_H0] / r
    pi = counts[Decision.INCONCLUSIVE] / r
    mean_n = sum_n / r
    var_n = max(sum_n2 / r - mean_n * mean_n, 0.0)
    return EmpiricalReport(
        kind="empirical",
        true_p=true_p,
        replications=r,
        accept_h1=p1,
        accept_h0=p0,
        inconclusive=pi,
        mean_n=mean_n,
        se_accept_h1=_se_prop(p1, r),
        se_accept_h0=_se_prop(p0, r),
        se_inconclusive=_se_prop(pi, r),
        se_mean_n=math.sqrt(var_n / r),
        base_seed=base_seed,
        seeds=tuple(seeds),
    )


class _FastBernoulli:
    """与 SeededBernoulli 同分布的轻量源（每轮独立 Random 实例）。"""

    def __init__(self, p: float, seed):
        self.p = p
        self.seed = seed
        self._rng = random.Random(seed)
        self.drawn = 0

    def sample(self) -> int:
        self.drawn += 1
        return 1 if self._rng.random() < self.p else 0
