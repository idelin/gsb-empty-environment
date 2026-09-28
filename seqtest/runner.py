"""随机源注入与单次运行。

随机性只能来自调用方注入的 sampler（必须有 sample() -> 0/1）。
SeededBernoulli 记录 seed；ScriptedSampler 用于确定性测试/重放。
"""
from __future__ import annotations

import random
from dataclasses import dataclass

from .evidence import Decision, Evidence
from .config import SprtConfig


class BernoulliSampler:
    def sample(self) -> int:
        raise NotImplementedError


class SeededBernoulli(BernoulliSampler):
    """带 seed 的伯努利随机源，记录 seed 与已产生样本量。"""

    def __init__(self, p: float, seed=None):
        if not 0.0 <= p <= 1.0:
            raise ValueError("p 必须在 [0, 1] 内")
        self.p = p
        self.seed = seed
        self.rng = random.Random(seed)
        self.drawn = 0

    def sample(self) -> int:
        self.drawn += 1
        return 1 if self.rng.random() < self.p else 0


class ScriptedSampler(BernoulliSampler):
    """按给定序列出数，可循环；用于精确对照测试。"""

    def __init__(self, seq, *, cyclic: bool = False, record: bool = True):
        self.seq = [int(x) for x in seq]
        self.cyclic = cyclic
        self.pos = 0
        self.record = record
        self.drawn = 0

    def sample(self) -> int:
        if self.pos >= len(self.seq):
            if not self.cyclic:
                raise RuntimeError("脚本样本已耗尽")
            self.pos = 0
        x = self.seq[self.pos]
        self.pos += 1
        self.drawn += 1
        return x


@dataclass(frozen=True)
class TrialRecord:
    decision: str
    n: int
    k: int
    log_lr: float
    interrupted: bool
    seed: object
    drawn: int


def run_once(config: SprtConfig, sampler, evidence: Evidence | None = None, *,
             sample_budget: int | None = None) -> TrialRecord:
    """运行一次序贯检验直到终止或预算中断。

    预算中断时不产生该次抽样（恢复后不重复计入样本），
    interrupted=True 且当前判定为 CONTINUE。
    """
    ev = evidence if evidence is not None else Evidence.fresh(config)
    used = ev.n
    while not Decision.is_terminal(ev.decision()):
        if sample_budget is not None and used >= sample_budget:
            return TrialRecord(ev.decision(), ev.n, ev.k, ev.log_lr, True,
                               getattr(sampler, "seed", None), getattr(sampler, "drawn", used))
        x = sampler.sample()
        used += 1
        ev.add_observation(x)
    return TrialRecord(ev.decision(), ev.n, ev.k, ev.log_lr, False,
                       getattr(sampler, "seed", None), getattr(sampler, "drawn", used))
