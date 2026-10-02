"""bootci 运行样例：python3.11 examples/run_examples.py"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from bootci import (
    BCA,
    PERCENTILE,
    DegenerateInterval,
    OneSample,
    PairedSample,
    StratifiedSample,
    TimeSeriesSample,
    TwoSample,
    bootstrap,
    exact_bootstrap,
)


def mean(v):
    return sum(v) / len(v)


def mean_diff(x, y):
    return mean(y) - mean(x)


def lag1_autocorr(v):
    n = len(v)
    m = mean(v)
    num = sum((v[i] - m) * (v[i - 1] - m) for i in range(1, n))
    den = sum((x - m) ** 2 for x in v)
    return num / den


def show(title, report):
    print(f"== {title} ==")
    print(f"  统计量: {report.statistic_name}  观测值: {report.estimate:.4f}")
    print(f"  性质: {report.nature}  seed: {report.master_seed}  "
          f"重采样次数: {report.n_resamples}  无效: {report.invalid_resamples}")
    print(f"  样本量: {report.sample_sizes}  alpha: {report.alpha}  "
          f"分位数定义: {report.quantile_method}")
    for method, iv in report.intervals.items():
        if isinstance(iv, DegenerateInterval):
            print(f"  [{method}] 退化区间 -> 点 {iv.point:.4f}（{iv.reason}）")
        else:
            print(f"  [{method}] {1 - iv.alpha:.0%} 区间: "
                  f"[{iv.low:.4f}, {iv.high:.4f}]")
    for w in report.warnings:
        print(f"  警告: {w}")
    print()


def main():
    # 1. 单样本均值：百分位 + BCa（经验估计）
    show("单样本均值（经验）", bootstrap(
        OneSample([2.1, 3.5, 2.8, 4.2, 3.0, 3.8, 2.5, 3.3], sample_id="ones"),
        mean, n_resamples=5000, seed=20261002, methods=(PERCENTILE, BCA),
    ))

    # 2. 配对样本：前后测量差值均值（成对抽取）
    before = (8.1, 7.5, 9.0, 6.8, 8.8, 7.9, 8.4, 7.2)
    after = (8.9, 7.8, 9.6, 7.5, 9.1, 8.0, 9.0, 7.9)
    show("配对样本差值均值（经验）", bootstrap(
        PairedSample(before, after, sample_id="paired"),
        mean_diff, n_resamples=5000, seed=20261002, methods=(PERCENTILE, BCA),
    ))

    # 3. 成组两样本：组间均值差（组内独立重采样）
    show("成组两样本均值差（经验）", bootstrap(
        TwoSample([5.1, 4.8, 5.5, 5.0, 4.6], [6.2, 5.8, 6.5, 6.0, 6.3],
                  sample_id="grp"),
        mean_diff, n_resamples=5000, seed=20261002, methods=(PERCENTILE, BCA),
    ))

    # 4. 分层样本：总体均值（层内重采样，层大小固定）
    show("分层总体均值（经验）", bootstrap(
        StratifiedSample(((1.0, 1.4, 1.2, 1.1), (4.8, 5.2, 5.0)),
                         sample_id="strat"),
        lambda strata: mean(strata[0] + strata[1]),
        n_resamples=5000, seed=20261002, methods=(PERCENTILE, BCA),
    ))

    # 5. 移动块 bootstrap：AR(1) 序列的滞后 1 阶自相关（块为重采样单位）
    rng_state, series = 0.0, []
    import random
    ar_rng = random.Random(123)
    for _ in range(60):
        rng_state = 0.7 * rng_state + ar_rng.gauss(0, 1)
        series.append(rng_state)
    show("AR(1) 滞后1阶自相关（移动块, circular, b=6）", bootstrap(
        TimeSeriesSample(tuple(series), sample_id="ar1"),
        lag1_autocorr, n_resamples=5000, seed=20261002,
        methods=(PERCENTILE,), block_length=6, boundary="circular",
    ))

    # 6. 退化分布：相同值样本 → 区间退化为一点，单独报告
    show("相同值样本（退化，单独报告）", bootstrap(
        OneSample([7.0] * 6, sample_id="const"),
        mean, n_resamples=1000, seed=20261002, methods=(PERCENTILE, BCA),
    ))

    # 7. 有限小样本精确枚举（精确结论，非经验估计）
    show("单样本均值 n=5（精确枚举）", exact_bootstrap(
        OneSample([1.0, 2.0, 3.0, 4.0, 5.0], sample_id="tiny"),
        mean, alpha=0.05,
    ))

    # 8. 并行顺序无关演示：同一 seed，不同处理顺序，结果一致
    def run(order):
        out = {}
        for sid, vals in order:
            out[sid] = bootstrap(OneSample(vals, sample_id=sid), mean,
                                 n_resamples=1000, seed=777,
                                 methods=(PERCENTILE,))
        return out

    a = run([("s1", (1.0, 2.0, 3.0)), ("s2", (4.0, 5.0, 6.0, 7.0))])
    b = run([("s2", (4.0, 5.0, 6.0, 7.0)), ("s1", (1.0, 2.0, 3.0))])
    same = all(a[k].intervals == b[k].intervals for k in a)
    print(f"== 并行顺序无关 ==\n  两种处理顺序结果一致: {same}")


if __name__ == "__main__":
    main()
