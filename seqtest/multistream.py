"""多流（多个并行序贯检验）共享停止预算。

两类共享预算：
  * sample_budget：所有流抽取的样本总数上限。某流要抽下一个样本但总预算已耗尽时
    返回 INTERRUPTED，不产生该次抽样；之后补充预算可恢复，恢复不重复计入样本。
  * family_alpha_cap：可选的族级一类错误上界。只有在此登记、在登记时就用精确
    递推核验过的“声明规则”才享受覆盖；未声明的选择性停止不在保证范围内。
"""
from __future__ import annotations

from dataclasses import dataclass

from .config import SprtConfig
from .evidence import Decision, Evidence
from .exact import exact_performance


@dataclass(frozen=True)
class DeclaredRule:
    stream: str
    config: SprtConfig
    alpha_allocated: float
    exact_alpha: float
    exact_beta: float


@dataclass(frozen=True)
class StreamStatus:
    stream: str
    decision: str
    n: int
    k: int
    interrupted: bool
    exact_alpha: float


class MultiStreamStudy:
    def __init__(self, *, sample_budget: int | None = None,
                 family_alpha_cap: float | None = None):
        if sample_budget is not None and sample_budget < 0:
            raise ValueError("sample_budget 不能为负")
        if family_alpha_cap is not None and not 0.0 <= family_alpha_cap <= 1.0:
            raise ValueError("family_alpha_cap 必须在 [0, 1] 内")
        self.sample_budget = sample_budget
        self.total_sampled = 0
        self.family_alpha_cap = family_alpha_cap
        self.alpha_allocated = 0.0
        self.rules: dict[str, DeclaredRule] = {}
        self.streams: dict[str, dict] = {}

    def declare(self, name: str, config: SprtConfig, alpha_allocation: float) -> DeclaredRule:
        """预先登记一条声明规则，并用精确递推核验其一类错误不超分配额。"""
        if name in self.streams:
            raise ValueError(f"流 {name!r} 已存在")
        if not 0.0 <= alpha_allocation <= 1.0:
            raise ValueError("alpha_allocation 必须在 [0, 1] 内")
        if self.family_alpha_cap is not None:
            if self.alpha_allocated + alpha_allocation > self.family_alpha_cap + 0.0:
                raise ValueError(
                    f"声明分配之和 {self.alpha_allocated + alpha_allocation} "
                    f"超过族上界 {self.family_alpha_cap}（Bonferroni 型预算）"
                )
        rep0 = exact_performance(config, config.p0)
        rep1 = exact_performance(config, config.p1)
        if rep0.accept_h1 > alpha_allocation + 1e-15:
            raise ValueError(
                f"规则 {name!r} 精确一类错误 {rep0.accept_h1:.6g} 超过分配额 {alpha_allocation:.6g}；"
                "不能作为声明规则登记（覆盖保证只给真正满足预算的规则）"
            )
        rule = DeclaredRule(name, config, alpha_allocation, rep0.accept_h1,
                            1.0 - rep1.accept_h1)
        self.rules[name] = rule
        self.alpha_allocated += alpha_allocation
        self.streams[name] = {
            "config": config,
            "evidence": Evidence.fresh(config),
            "sampler": None,
            "interrupted": False,
            "exact_alpha": rep0.accept_h1,
        }
        return rule

    def attach_sampler(self, name: str, sampler) -> None:
        self._require(name)
        self.streams[name]["sampler"] = sampler

    def _require(self, name: str) -> dict:
        if name not in self.streams:
            raise KeyError(f"未知流 {name!r}（未声明的流不在覆盖保证内）")
        return self.streams[name]

    def step(self, name: str) -> StreamStatus:
        """推进一步；若终止则直接返回，若总预算耗尽则不抽样并标记中断。"""
        st = self._require(name)
        ev: Evidence = st["evidence"]
        decision = ev.decision()
        if Decision.is_terminal(decision):
            return StreamStatus(name, decision, ev.n, ev.k, False, st["exact_alpha"])
        if self.sample_budget is not None and self.total_sampled >= self.sample_budget:
            st["interrupted"] = True
            return StreamStatus(name, Decision.CONTINUE, ev.n, ev.k, True, st["exact_alpha"])
        if st["sampler"] is None:
            raise RuntimeError(f"流 {name!r} 尚未注入随机源")
        x = st["sampler"].sample()
        self.total_sampled += 1
        st["interrupted"] = False
        ev.add_observation(x)
        return StreamStatus(name, ev.decision(), ev.n, ev.k, False, st["exact_alpha"])

    def run(self, name: str) -> StreamStatus:
        st = self._require(name)
        while True:
            status = self.step(name)
            if Decision.is_terminal(status.decision) or status.interrupted:
                return status

    def run_all(self) -> dict[str, StreamStatus]:
        out = {}
        for name in self.streams:
            out[name] = self.run(name)
        return out

    def refill_budget(self, extra: int) -> None:
        """补充共享样本预算（中断恢复，不重复计入任何已抽样本）。"""
        if extra < 0:
            raise ValueError("extra 不能为负")
        if self.sample_budget is None:
            self.sample_budget = self.total_sampled + extra
        else:
            self.sample_budget += extra

    def snapshot(self) -> dict:
        """checkpoint：预算、总样本量、各流证据。"""
        return {
            "sample_budget": self.sample_budget,
            "total_sampled": self.total_sampled,
            "family_alpha_cap": self.family_alpha_cap,
            "alpha_allocated": self.alpha_allocated,
            "streams": {
                name: {
                    "evidence": st["evidence"].snapshot(),
                    "interrupted": st["interrupted"],
                    "exact_alpha": st["exact_alpha"],
                }
                for name, st in self.streams.items()
            },
        }

    def coverage_statement(self) -> dict:
        """覆盖保证声明：只覆盖这里列出的声明规则。"""
        exact_sum = sum(rule.exact_alpha for rule in self.rules.values())
        guaranteed = (self.family_alpha_cap is not None and exact_sum <= self.family_alpha_cap)
        return {
            "scope": "仅覆盖下列预先声明并经精确递推核验的规则；未声明的选择性停止不享受任何覆盖保证",
            "family_alpha_cap": self.family_alpha_cap,
            "sum_allocated": self.alpha_allocated,
            "sum_exact_alpha": exact_sum,
            "union_bound_fwer": exact_sum,
            "family_wise_guarantee_holds": guaranteed,
            "bonferroni_bound": self.alpha_allocated if self.family_alpha_cap is not None else None,
            "rules": [
                {
                    "stream": r.stream,
                    "alpha_allocated": r.alpha_allocated,
                    "exact_alpha": r.exact_alpha,
                    "exact_beta": r.exact_beta,
                }
                for r in self.rules.values()
            ],
        }
