"""增量分层统计:样本身份变更时增量撤回/追加贡献,无需重存全部数据。

每个层只维护 (count, sum, sumsq) 三个充分统计量:
- add(stratum, value):     新样本成员入样,追加贡献;
- retract(stratum, value): 样本身份变更,精确撤回旧成员贡献;
- estimate():              由当前充分统计量重建 StratumSample 并复用
                           estimate_stratified_mean,保证与批量计算一致。
"""

from __future__ import annotations

from dataclasses import dataclass

from .stratified import (
    SamplingDesignError,
    StratifiedEstimate,
    StratumSample,
    estimate_stratified_mean,
)


@dataclass
class _SufficientStats:
    count: int = 0
    total: float = 0.0
    sumsq: float = 0.0

    def values(self) -> tuple[float, ...]:
        """由充分统计量还原均值与(若可估的)离差信息。

        不保存原始观测;为了复用批量估计器,这里用均值与方差等价地
        重建一组代表值(均值相同、离差平方和相同),估计结果与原始
        数据完全一致。
        """
        if self.count == 0:
            return ()
        mu = self.total / self.count
        if self.count == 1:
            return (mu,)
        ss = max(self.sumsq - self.count * mu * mu, 0.0)
        # 构造 1 个偏离点与 (count-1) 个对称点:离差为 d 与 -d/(count-1),
        # 离差平方和 = d^2 * count/(count-1) = ss。
        d = (ss * (self.count - 1) / self.count) ** 0.5
        return (mu + d,) + (mu - d / (self.count - 1),) * (self.count - 1)


class IncrementalStratifiedEstimator:
    """支持增量撤回的分层均值/方差估计器。"""

    def __init__(self, pop_sizes: dict[str, int]) -> None:
        if not pop_sizes:
            raise SamplingDesignError("至少需要一个层")
        for name, n_pop in pop_sizes.items():
            if n_pop < 1:
                raise SamplingDesignError(f"层 {name!r} 的总体量必须 >= 1")
        self._pop_sizes = dict(pop_sizes)
        self._stats: dict[str, _SufficientStats] = {
            name: _SufficientStats() for name in pop_sizes
        }

    def add(self, stratum: str, value: float) -> None:
        st = self._stats[stratum]
        if st.count + 1 > self._pop_sizes[stratum]:
            raise SamplingDesignError(
                f"层 {stratum!r} 样本量将超过总体量 {self._pop_sizes[stratum]}"
            )
        st.count += 1
        st.total += value
        st.sumsq += value * value

    def retract(self, stratum: str, value: float) -> None:
        """样本身份变更时,精确撤回该成员的统计贡献。"""
        st = self._stats[stratum]
        if st.count == 0:
            raise SamplingDesignError(f"层 {stratum!r} 没有可撤回的样本")
        st.count -= 1
        st.total -= value
        st.sumsq -= value * value
        if st.sumsq < 0 and st.sumsq > -1e-9:
            st.sumsq = 0.0  # 浮点噪声归零

    def sample_sizes(self) -> dict[str, int]:
        return {name: st.count for name, st in self._stats.items()}

    def estimate(self) -> StratifiedEstimate:
        strata = [
            StratumSample(
                name=name,
                pop_size=self._pop_sizes[name],
                values=st.values(),
            )
            for name, st in self._stats.items()
            if st.count > 0
        ]
        return estimate_stratified_mean(strata)
