"""运行样例：python3 -m gdc.main

演示：本地夹具消费 -> 输入去重 -> 组迁移 -> 溢写 -> checkpoint/恢复 ->
独立全量参考核验 -> 缺事件（负计数）拒绝。
"""
from __future__ import annotations

import os
import shutil

from .aggregator import DistinctConfig, GroupedDistinctCounter, MissingEventError, NullPolicy
from .events import ChangeEvent
from .fixtures import load_events
from .reference import ReferenceModel

OUT_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "out")


def main() -> None:
    shutil.rmtree(OUT_DIR, ignore_errors=True)
    os.makedirs(OUT_DIR, exist_ok=True)
    spill_dir = os.path.join(OUT_DIR, "spill")
    checkpoint_path = os.path.join(OUT_DIR, "checkpoint.pkl")

    config = DistinctConfig(
        table="users",
        group_cols=("country",),
        value_cols=("email",),
        null_policy=NullPolicy.COUNT_AS_VALUE,  # null 作为独立 distinct 值
        null_equal=True,
        spill_threshold=8,  # 调小阈值以演示溢写
    )
    counter = GroupedDistinctCounter(config, spill_dir=spill_dir)
    reference = ReferenceModel(config)

    fixture = os.path.join(os.path.dirname(OUT_DIR), "fixtures", "sample_events.json")
    events = load_events(fixture)

    # 阶段 1：夹具事件（含重复投递 e3、跨组迁移 e6、删最后支持者 e7/e8、异表 e9）
    for event in events:
        counter.apply(event)
        reference.apply(event)
    print("== 阶段1 夹具事件 ==", counter.stats)

    # 阶段 2：批量插入触发 DE 组溢写
    for i in range(20):
        e = ChangeEvent(
            event_id=f"b{i}", tx_id="txb", table="users", pk=(1000 + i,),
            op="insert", before=None,
            after={"id": 1000 + i, "country": "DE", "email": f"u{i}@x.de"},
            commit_seq=100 + i,
        )
        counter.apply(e)
        reference.apply(e)
    print("== 阶段2 溢写文件 ==", sorted(os.listdir(spill_dir)) or "(无)")

    # 阶段 3：删除部分行（会按需载入溢写组）
    for i in range(5):
        e = ChangeEvent(
            event_id=f"bd{i}", tx_id="txbd", table="users", pk=(1000 + i,),
            op="delete", before={"id": 1000 + i, "country": "DE", "email": f"u{i}@x.de"},
            after=None, commit_seq=200 + i,
        )
        counter.apply(e)
        reference.apply(e)

    print("== distinct 数 ==")
    for group, n in sorted(counter.distinct_counts().items()):
        print(f"  {group}: {n}")
    print("== 支持计数快照 ==")
    for group, counts in sorted(counter.snapshot().items()):
        print(f"  {group}: {counts}")

    # checkpoint -> 全新实例恢复（保留支持计数与输入去重，而非输出计数）
    counter.checkpoint(checkpoint_path)
    restored = GroupedDistinctCounter.restore(checkpoint_path, config, spill_dir=spill_dir)
    dup = events[2]  # 重放已处理事件，验证去重集合随 checkpoint 恢复
    assert restored.apply(dup) is False
    print("== 恢复后重放旧事件 == 已按去重跳过, stats =", restored.stats)

    # 独立全量参考核验（原始与恢复后的聚合器都要一致）
    for name, c in [("原始", counter), ("恢复", restored)]:
        problems = reference.verify(c)
        print(f"== 参考核验（{name}聚合器）==", "一致" if not problems else problems)

    # 缺事件演示：删除一条从未插入的行 -> 计数将变负，拒绝
    bogus = ChangeEvent(
        event_id="bogus1", tx_id="txx", table="users", pk=(777,),
        op="delete", before={"id": 777, "country": "CN", "email": "ghost@x.cn"},
        after=None, commit_seq=999,
    )
    try:
        restored.apply(bogus)
        print("== 缺事件检测 == 未触发（异常！）")
    except MissingEventError as exc:
        print("== 缺事件检测 == 已拒绝:", exc)


if __name__ == "__main__":
    main()
