"""cbound 运行样例（合成数据）。"""

from cbound import ContributionBounder, Params, Record, check_l1_bound

PARAMS = Params(max_records_per_subject=2, max_groups_per_subject=2,
                lower=-5.0, upper=5.0)


def show_evidence(title, bounder):
    print(f"\n== {title} ==")
    print(f"{'record':<8}{'subject':<9}{'group':<6}{'raw':>7}{'clipped':>9}"
          f"  {'status':<9} drop_reason")
    for e in bounder.clipping_evidence():
        print(f"{e['record_id']:<8}{e['subject_id']:<9}{e['group']:<6}"
              f"{e['raw_value']:>7.1f}{e['clipped_value']:>9.1f}"
              f"  {e['status']:<9} {e['drop_reason']}")


def main():
    print(f"参数: {PARAMS}")
    print(f"单主体 L1 求和贡献确定性上界: {PARAMS.l1_sum_bound}")

    # 分片 A / B：主体 s1 跨两片，含越界负值、越界正值、跨分片重复 ID
    shard_a = [
        Record("r1", "s1", "g1", 3.0),
        Record("r2", "s1", "g1", -99.0),   # 负值越界
        Record("r3", "s1", "g2", 4.0),
        Record("r4", "s2", "g1", 2.0),
    ]
    shard_b = [
        Record("r5", "s1", "g3", 1.0),     # s1 的第 3 个组
        Record("r6", "s2", "g1", 88.0),    # 正值越界
        Record("r2", "s1", "g1", -99.0),   # 换分片重复提交 -> 去重
    ]
    left, right = ContributionBounder(PARAMS), ContributionBounder(PARAMS)
    left.ingest_shard(shard_a, shard="A")
    right.ingest_shard(shard_b, shard="B")

    merged = left.merge(right)
    print(f"\n合并事件: {merged.audit[-1]}")
    print(f"合并状态大小: {merged.state_size_bytes()} 字节")
    show_evidence("合并后每条记录的裁剪证据", merged)
    print(f"保留记录: {[r.record_id for r in merged.retained()]}")
    print(f"裁剪后求和: {merged.aggregate_sum()}")

    # 撤回 r1 -> r3 补回
    merged.retract("r1")
    show_evidence("撤回 r1 后（候选补回）", merged)
    print(f"保留记录: {[r.record_id for r in merged.retained()]}")

    # 更正 r4 为越界值
    merged.correct(Record("r4", "s2", "g1", 500.0))
    show_evidence("更正 r4=500 后", merged)
    print(f"裁剪后求和: {merged.aggregate_sum()}")

    # 小域枚举：相邻主体数据的实际 L1 贡献
    domain = {
        "s1": [Record("a1", "s1", "g1", 7.0), Record("a2", "s1", "g1", -8.0),
               Record("a3", "s1", "g2", 3.0)],
        "s2": [Record("b1", "s2", "g1", -4.0), Record("b2", "s2", "g2", 9.0)],
        "s3": [Record("c1", "s3", "g1", 2.0)],
    }
    result = check_l1_bound(domain, PARAMS)
    print("\n== 小域枚举 L1 检查（非形式化隐私证明） ==")
    for k in ("subjects", "neighboring_pairs_checked", "actual_max_l1",
              "deterministic_bound", "bound_holds"):
        print(f"  {k}: {result[k]}")


if __name__ == "__main__":
    main()
