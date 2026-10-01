"""运行样例：合成数据上的等价类不同数与熵指标全流程演示。

仅使用合成数据；本演示是统计实验，不构成任何形式化隐私证明。
运行：python3.11 demo.py
"""

import random

from equivstats import (
    Dataset,
    Hierarchy,
    NullPolicy,
    Record,
    decide_entropy,
    evaluate_dataset,
    exact_min_suppression,
    greedy_suppression,
    verify_incremental,
    verify_shard_merge,
)

LOG_BASE = 2
MIN_DISTINCT = 2
MIN_ENTROPY = 1.0
NULL_POLICY = NullPolicy.AS_CATEGORY


def make_synthetic_records(seed=20261001):
    rng = random.Random(seed)
    qids = [("30s", "100"), ("30s", "101"), ("40s", "200")]
    diseases = ["flu", "covid", "cold", "asthma", None]
    weights = [0.30, 0.25, 0.20, 0.15, 0.10]
    records = []
    for qid in qids:
        for _ in range(rng.randint(6, 10)):
            records.append(Record(qid, rng.choices(diseases, weights)[0]))
    # 人为制造一个熵不达标的偏斜类
    records += [Record(("50s", "300"), "flu") for _ in range(8)]
    records += [Record(("50s", "300"), "cold"), Record(("50s", "300"), None)]
    return records


def show_report(title, dataset):
    print(f"\n== {title} ==")
    reports = evaluate_dataset(dataset, MIN_DISTINCT, MIN_ENTROPY)
    for report in reports:
        status = "FAIL" if report.failing else "pass"
        print(f"  类 {report.qid} [{status}] 总数={report.total} "
              f"不同数={report.distinct}(阈值>={MIN_DISTINCT}) "
              f"熵=[{float(report.entropy.lo):.6f}, {float(report.entropy.hi):.6f}]"
              f"(阈值>={MIN_ENTROPY}, 判定={report.entropy_verdict.value})")
        if report.failing:
            for c in report.contributions:
                print(f"    贡献 值={c.value!r} 计数={c.count} "
                      f"比例={float(c.proportion):.4f} "
                      f"熵贡献≈{float(c.entropy_contribution.midpoint):.6f}")
    return reports


def main():
    print(f"配置: null策略={NULL_POLICY.value}, 对数底={LOG_BASE}, "
          f"不同数阈值={MIN_DISTINCT}, 熵阈值={MIN_ENTROPY}")

    records = make_synthetic_records()
    dataset = Dataset(null_policy=NULL_POLICY, log_base=LOG_BASE)
    dataset.add_many(records)
    print(f"合成记录数: {len(records)}")

    show_report("初始评估", dataset)

    # 浮点熵接近阈值：误差区间判定，无法区分则未决
    from collections import Counter
    interval, verdict = decide_entropy(Counter({"a": 5, "b": 5}), NULL_POLICY, 2, 1.0)
    print(f"\n== 边界判定示例 ==\n  H=1.0 恰好等于阈值 1.0 -> "
          f"区间[{interval.lo}, {interval.hi}] 判定={verdict.value}（未决）")

    # 层级合并：保留原贡献，可撤回
    hierarchy = Hierarchy({"flu": "resp", "covid": "resp", "cold": "resp",
                           "asthma": "chronic", "resp": "disease",
                           "chronic": "disease"})
    values = {r.sensitive for r in records if r.sensitive is not None}
    dataset.generalize(hierarchy.mapping_at_level(values, 1))
    show_report("层级合并 level=1 后", dataset)
    dataset.rollback()
    print("\n撤回合并后计数恢复原状:", verify_incremental(dataset))

    # 增量删除并独立重算校验
    victim = next(r for r in dataset.records if r.qid == ("50s", "300")
                  and r.sensitive == "flu")
    dataset.remove(victim)
    print(f"增量删除 {victim} 后与全量重算一致:", verify_incremental(dataset))

    # 分片合并校验
    shards = [records[0:9], records[9:19], records[19:]]
    print("分片合并与直接重算一致:", verify_shard_merge(shards))

    # 最少抑制记录（小域枚举）+ 贪心对照
    small = [r for r in records if r.qid in (("50s", "300"), ("30s", "100"))]
    exact = exact_min_suppression(small, MIN_DISTINCT, MIN_ENTROPY,
                                  NULL_POLICY, LOG_BASE)
    greedy = greedy_suppression(small, MIN_DISTINCT, MIN_ENTROPY,
                                NULL_POLICY, LOG_BASE)
    print(f"\n== 抑制搜索（子集 {len(small)} 条）==")
    print(f"  最少抑制(枚举): {len(exact)} 条 -> "
          f"{[str(small[i]) for i in exact]}")
    print(f"  贪心抑制:       {len(greedy)} 条")


if __name__ == "__main__":
    main()
