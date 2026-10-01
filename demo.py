"""运行样例：等价类指标评估、层级合并/撤回、增量删除验证、
分片合并验证、最少抑制搜索。

仅使用合成数据；输出为统计实验结果，不构成形式隐私证明。
"""
from __future__ import annotations

from collections import defaultdict

from hierarchy import Hierarchy
from metrics import ClassStats, Config, NullPolicy, Status, recompute_stats
from suppression import (
    is_feasible,
    min_suppression_exact,
    min_suppression_greedy,
    total_deletions,
)
from synthetic import DISEASE_HIERARCHY, generate_records

DISCLAIMER = "说明：本演示仅使用合成数据；统计实验结果不作为形式隐私证明。"


def build_classes(records, config):
    grouped = defaultdict(list)
    for record in records:
        grouped[record.class_key].append(record.sensitive)
    return grouped


def show_evaluation(grouped, config):
    print("== 等价类评估 ==")
    print(
        f"参数: distinct>={config.distinct_threshold}, "
        f"entropy>={config.entropy_threshold} (log base {config.log_base}), "
        f"null_policy={config.null_policy.value}, "
        f"count_all_null_class={config.count_all_null_class}"
    )
    stats_by_key = {}
    for key in sorted(grouped, key=repr):
        stats = recompute_stats(grouped[key], config, class_key=key)
        stats_by_key[key] = stats
        report = stats.evaluate()
        interval = report.entropy_interval
        print(
            f"类 {key}: n={report.n} distinct={report.distinct} "
            f"H={report.entropy:.4f} H区间=[{interval.lo:.6f},{interval.hi:.6f}] "
            f"状态={report.status.value}"
        )
    failed = [
        (k, s.evaluate())
        for k, s in stats_by_key.items()
        if s.evaluate().status in (Status.FAIL, Status.UNDECIDED)
    ]
    print("\n== 失败/未决类及各项贡献 ==")
    if not failed:
        print("（无）")
    for key, report in failed:
        print(f"类 {key}: 状态={report.status.value} 未达标项={report.failed_metrics or ('entropy未决',)}")
        for value, contrib in sorted(
            report.contributions.items(), key=lambda kv: repr(kv[0])
        ):
            print(
                f"    值={value!r}: count={contrib.count} "
                f"share={contrib.share:.3f} 熵贡献={contrib.entropy_term:.4f}"
            )
    return stats_by_key


def show_hierarchy(stats_by_key, config):
    print("\n== 层级合并与撤回 ==")
    hierarchy = Hierarchy(DISEASE_HIERARCHY)
    key = next(k for k, s in stats_by_key.items() if s.distinct_count() >= 3)
    stats = stats_by_key[key]
    before = stats.evaluate()
    mapping = hierarchy.merge_mapping(stats.counts)
    stats.merge_values(mapping)
    after = stats.evaluate()
    print(
        f"类 {key}: 合并 {mapping} 后 distinct {before.distinct}->{after.distinct}, "
        f"H {before.entropy:.4f}->{after.entropy:.4f}"
    )
    restored = stats.rollback()
    print(f"撤回后 distinct={stats.distinct_count()}，恢复原贡献 {len(restored)} 项")


def show_incremental_delete(grouped, config):
    print("\n== 增量删除与独立重算验证 ==")
    key = sorted(grouped, key=repr)[0]
    records = list(grouped[key])
    stats = recompute_stats(records, config, class_key=key)
    removed = records[:3]
    remaining = records[3:]
    for value in removed:
        stats.delete(value)
    ok = stats.verify(remaining)
    print(f"类 {key}: 删除 {len(removed)} 条后增量统计与独立重算一致: {ok}")


def show_shard_merge(grouped, config):
    print("\n== 分片合并验证 ==")
    key = sorted(grouped, key=repr)[0]
    records = list(grouped[key])
    shards = [records[0::3], records[1::3], records[2::3]]
    merged = ClassStats(config, class_key=key)
    for shard in shards:
        merged.merge_shard(recompute_stats(shard, config))
    baseline = recompute_stats(records, config)
    ok = dict(merged.counts) == dict(baseline.counts) and abs(
        merged.entropy() - baseline.entropy()
    ) <= 1e-9
    print(f"类 {key}: 3 个分片合并后与整体重算一致: {ok}")


def show_suppression(stats_by_key, config):
    print("\n== 最少抑制记录 ==")
    for key in sorted(stats_by_key, key=repr):
        stats = stats_by_key[key]
        if is_feasible(stats.counts, config):
            continue
        exact = min_suppression_exact(stats.counts, config)
        greedy = min_suppression_greedy(stats.counts, config)
        print(
            f"类 {key}: 精确解抑制 {total_deletions(exact)} 条 {exact}; "
            f"贪心解抑制 {total_deletions(greedy)} 条 {greedy}"
        )


def main():
    print(DISCLAIMER)
    config = Config(
        distinct_threshold=3,
        entropy_threshold=1.0,
        log_base=2.0,
        null_policy=NullPolicy.IGNORE,
        count_all_null_class=False,
    )
    records = generate_records(seed=42)
    grouped = build_classes(records, config)
    stats_by_key = show_evaluation(grouped, config)
    show_hierarchy(stats_by_key, config)
    show_incremental_delete(grouped, config)
    show_shard_merge(grouped, config)
    show_suppression(stats_by_key, config)


if __name__ == "__main__":
    main()
