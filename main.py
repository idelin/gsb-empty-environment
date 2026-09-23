"""运行样例：本地夹具 -> 引擎 -> checkpoint -> 恢复续跑 -> 独立参考核验。"""
import json
import os
import random
import tempfile

from distinct_agg import DistinctCountEngine, load_events

CONFIG = dict(
    group_cols=["dept"],
    distinct_cols=["tag"],
    null_equal=True,
    filters=[("score", "ge", 3)],  # 条件过滤：score < 3 的行不产生贡献
)


def pretty(counts):
    return {json.loads(g)[0]: n for g, n in sorted(counts.items())}


def main():
    events = load_events("fixtures/changes.jsonl")
    split = 5  # 前 5 条后直接 checkpoint

    # 路径一：一次性跑完
    full = DistinctCountEngine(**CONFIG)
    stats = full.apply_all(events)

    # 路径二：中途 checkpoint，恢复后续跑
    ckpt = os.path.join(tempfile.mkdtemp(), "state.ckpt.sqlite")
    part = DistinctCountEngine(**CONFIG)
    for e in events[:split]:
        part.apply(e)
    part.checkpoint(ckpt)
    restored = DistinctCountEngine.restore(ckpt)
    restored.apply_all(events[split:])

    # 独立全量参考核验：乱序 + 重复投递
    shuffled = events + [events[2], events[0]]
    random.Random(42).shuffle(shuffled)

    print("== 输入统计 ==", stats)
    print("== 一次性跑完 ==", pretty(full.snapshot_counts()))
    print("== checkpoint 恢复续跑 ==", pretty(restored.snapshot_counts()))
    print("== 两路径一致 ==", full.snapshot_counts() == restored.snapshot_counts())
    print("== 参考核验(乱序+重复) ==",
          full.verify(shuffled) and restored.verify(shuffled))
    g, v = '["ops"]', '["\\u0000NULL"]'
    print("== ops 组 null 值的支持计数 ==", full.support_count(g, v),
          "(D、F 两行同值 null，distinct 仍计 1)")


if __name__ == "__main__":
    main()
