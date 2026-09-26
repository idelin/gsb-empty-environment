"""运行样例：合成 UTF-8 文档流上的趋势排名 + 独立全量重算校验。

场景：桶 = 1 天；基准窗 = 第 10..16 天，近期窗 = 第 17..23 天。
演示：迟到文档、同 id 修订、重复事件、时间桶撤回、滑窗推进、空窗保护。
"""

from trend import TrendIndex
from trend.checker import full_recompute_check

DAY = 86400


def ts(day: int, sec: int = 0) -> int:
    return day * DAY + sec


def show(title, records):
    print(f"\n== {title} ==")
    if not records:
        print("  (空排名)")
        return
    print(f"  {'词':<8} {'DF近期':>6} {'DF基准':>6} {'近期分母':>8} {'基准分母':>8} {'平滑分':>10}")
    for r in records:
        print(f"  {r.term:<8} {r.df_recent:>6} {r.df_base:>6} "
              f"{r.recent_docs:>8} {r.base_docs:>8} {r.score:>10.4f}")


def main():
    idx = TrendIndex(bucket_size=DAY, alpha=1.0, min_support=2)

    # 基准窗（第 10..16 天）：旧热点
    for i in range(5):
        idx.add_document(f"base-apple-{i}", ts(10 + i % 7), "Apple pie recipe, apple sauce")
        idx.add_document(f"base-banana-{i}", ts(11 + i % 6), "banana smoothie")
    for i in range(3):
        idx.add_document(f"base-quantum-{i}", ts(12 + i), "量子 计算 进展缓慢")

    # 近期窗（第 17..23 天）：新热点；中文 UTF-8 文本
    for i in range(6):
        idx.add_document(f"recent-quantum-{i}", ts(17 + i % 7), "量子 计算 突破！量子 纠缠")
    for i in range(4):
        idx.add_document(f"recent-fusion-{i}", ts(18 + i % 5), "核聚变 点火 成功")
    for i in range(2):
        idx.add_document(f"recent-apple-{i}", ts(19 + i), "apple vision review")
    idx.add_document("recent-oneoff-0", ts(20), "彗星 掠过 地球")  # 支持数不足，应被过滤

    # 迟到文档：时间戳落在基准窗，修订对应的历史桶
    idx.add_document("late-quantum-0", ts(13, 3600), "量子 通信 迟到 报道")
    # 重复事件：同 id 同内容再次到达，幂等忽略
    idx.add_document("recent-quantum-0", ts(17), "量子 计算 突破！量子 纠缠")
    # 同 id 修订：内容变化，撤旧桶入新桶
    idx.add_document("recent-fusion-0", ts(18), "核聚变 点火 成功（修订版）")
    # 时间桶撤回
    idx.add_document("retracted-0", ts(20), "谣言 撤回前")
    idx.retract_document("retracted-0")

    base_win = (ts(10), ts(17))
    recent_win = (ts(17), ts(24))
    print(f"重复事件数={idx.duplicate_events} 修订数={idx.revisions}")
    print(f"基准窗分母={idx.window_stats(*base_win)[0]} 近期窗分母={idx.window_stats(*recent_win)[0]}")

    trends = idx.top_trends(base_win, recent_win, k=5)
    show("趋势 Top5（近期窗 vs 基准窗）", trends)
    print(f"  TopK 剪枝剪掉的候选数={idx.pruned_candidates}")

    # 滑窗推进：两个窗口同时前移 7 天
    slid = idx.top_trends((ts(17), ts(24)), (ts(24), ts(31)), k=5)
    show("滑窗推进后（新近期窗为空 -> 不伪报增长）", slid)

    # 独立全量重算校验
    report = full_recompute_check(idx, base_win, recent_win, k=5)
    print("\n== 独立全量重算校验 ==")
    print(f"  参与重算文档数={report.docs_checked} 重复事件数={report.duplicate_events}")
    if report.ok:
        print("  校验通过：分母、零窗、逐词 DF、排名均与增量结果一致")
    else:
        for c in report.corrections:
            print(f"  更正[{c.kind}] 词={c.term} 增量={c.incremental} 重算={c.recomputed}")
            print(f"    贡献证据={c.evidence}")


if __name__ == "__main__":
    main()
