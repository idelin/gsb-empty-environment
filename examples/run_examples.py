"""运行样例:分层估计、两阶段成组估计、增量撤回、精确枚举与经验模拟。

运行: python3.11 examples/run_examples.py
"""

import random
import sys
from pathlib import Path
from statistics import fmean

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from stratified_sampling import (
    IncrementalStratifiedEstimator,
    StratumSample,
    draw_stratified_sample,
    draw_two_stage_sample,
    estimate_stratified_mean,
    estimate_two_stage_mean,
    enumerate_stratified,
    enumerate_two_stage,
)


def show_estimate(title, est):
    print(f"\n== {title} ==")
    print(f"均值估计: {est.mean:.4f}")
    if est.variance_estimable:
        print(f"方差估计: {est.variance:.6f}  标准误: {est.standard_error:.6f}")
    else:
        print("方差估计: 不可估(不伪造精确方差)")
    for d in est.diagnostics:
        print(f"  诊断[{d.stratum}]: {d.reason}")


def main():
    # 1. 分层估计:层权按 N_h 加权;含全查层(方差贡献 0)。
    print("=" * 60)
    print("1. 分层均值/方差估计(已知 N_h 与 n_h)")
    est = estimate_stratified_mean([
        StratumSample("小层", 2, (10.0, 14.0)),                 # 全查,贡献 0
        StratumSample("大层", 8, (2.0, 4.0, 6.0, 8.0)),
    ])
    show_estimate("含全查层的分层估计", est)
    print(f"层权(和为 1): {est.weights}")

    # 2. 单样本层:均值照算,方差不可估。
    est1 = estimate_stratified_mean([
        StratumSample("单样本层", 100, (2.5,)),
        StratumSample("正常层", 100, (1.0, 3.0, 5.0)),
    ])
    show_estimate("含单样本层的分层估计", est1)

    # 3. 两阶段分层成组抽样(随机源由调用方注入,seed 被记录)。
    print("\n" + "=" * 60)
    print("2. 两阶段分层成组抽样(注入随机源)")
    pop2 = {
        "城东": {"c1": [1.0, 3.0], "c2": [5.0, 7.0], "c3": [2.0, 8.0]},
        "城西": {"d1": [4.0, 6.0], "d2": [10.0, 12.0]},
    }
    seed = 20261001
    rng = random.Random(seed)
    strata, record = draw_two_stage_sample(
        pop2, {"城东": 2, "城西": 1}, elements_per_cluster=2, rng=rng, seed=seed
    )
    est2 = estimate_two_stage_mean(strata)
    show_estimate("两阶段估计(城西仅 1 群入样)", est2)
    print(f"抽样记录: seed={record.seed}, 方法={record.method}, "
          f"各层入样群数={record.sample_sizes}, 群内样本量={record.detail}")

    # 4. 增量撤回:样本身份变更。
    print("\n" + "=" * 60)
    print("3. 增量统计:样本身份变更时撤回贡献")
    inc = IncrementalStratifiedEstimator({"A": 10, "B": 20})
    for v in (1.0, 2.0, 4.0):
        inc.add("A", v)
    for v in (3.0, 5.0, 7.0, 9.0):
        inc.add("B", v)
    print(f"变更前均值: {inc.estimate().mean:.4f}, 样本量: {inc.sample_sizes()}")
    inc.retract("A", 2.0)   # 成员出样,撤回其统计贡献
    inc.add("A", 10.0)      # 新成员入样
    print(f"变更后均值: {inc.estimate().mean:.4f}, 样本量: {inc.sample_sizes()}")

    # 5. 精确核验:有限总体全设计枚举。
    print("\n" + "=" * 60)
    print("4. 全设计枚举核验【精确结论】")
    pop_small = {"A": [1.0, 2.0, 3.0, 4.0], "B": [2.0, 4.0, 6.0, 8.0, 10.0, 12.0]}
    res = enumerate_stratified(pop_small, {"A": 2, "B": 3})
    print(f"[{res.label}] 设计数={res.n_designs}")
    print(f"  真值={float(res.true_mean):.4f}  E[估计]={float(res.expectation):.4f}"
          f"  无偏={res.unbiased}")
    print(f"  Var[估计]={float(res.variance):.6f}  公式方差={float(res.formula_variance):.6f}"
          f"  一致={res.variance_matches_formula}")
    res2 = enumerate_two_stage(pop2, {"城东": 2, "城西": 1}, elements_per_cluster=2)
    print(f"[{res2.label}] 两阶段设计数={res2.n_designs}  无偏={res2.unbiased}"
          f"  方差公式一致={res2.variance_matches_formula}")

    # 6. 经验核验:模拟(与精确结论分开标注)。
    print("\n" + "=" * 60)
    print("5. 模拟核验【经验结论】")
    rng = random.Random(42)
    reps = 4000
    estimates = []
    for _ in range(reps):
        samples, _ = draw_stratified_sample(pop_small, {"A": 2, "B": 3}, rng)
        estimates.append(estimate_stratified_mean(samples).mean)
    true_mean = fmean(y for vs in pop_small.values() for y in vs)
    print(f"[经验(模拟)] reps={reps}, seed=42")
    print(f"  真值={true_mean:.4f}  估计均值={fmean(estimates):.4f}(应接近真值)")


if __name__ == "__main__":
    main()
