"""演示：更正事件流如何让消费方恢复并维护"当前对齐表"。

场景即题目例子：主传感器 t=10 采一次，辅助传感器先有 t=8、t=12，
容差 2 → 选中 8；随后 t=9 迟到 → 替换为 9；再演示撤回回退与水位线最终化。
"""
from stream_align import AlignmentEngine, AlignmentConsumer


def show(consumer, tag):
    print(f"  [{tag}] 当前对齐表:")
    if not consumer.table:
        print("    (空)")
    for aid, row in sorted(consumer.table.items()):
        flag = " finalized" if row["finalized"] else ""
        print(f"    {aid} @t={row['anchor_time']:<5} matches={row['matches']} "
              f"missing={row['missing']} v{row['version']}{flag}")


def main():
    eng = AlignmentEngine(tolerance=2, sources=["aux"])
    consumer = AlignmentConsumer()

    def step(tag, events):
        print(f"{tag}")
        for ev in events:
            if ev.kind == "upsert":
                print(f"  <- UPSERT   {ev.anchor_id} v{ev.version} matches={ev.matches}")
            elif ev.kind == "retract":
                print(f"  <- RETRACT  {ev.anchor_id} (撤销该行)")
            else:
                print(f"  <- FINALIZE {ev.anchor_id} v{ev.version} matches={ev.matches}")
        consumer.apply_all(events)
        show(consumer, "consumer")

    step("1. 主锚点 m1@10 到达", eng.add_anchor("m1", 10, seq=1))
    step("2. 辅助样本 s8@8、s12@12 到达（同距，取较早的 s8）",
         eng.add_sample("aux", "s8", 8, seq=1) + eng.add_sample("aux", "s12", 12, seq=2))
    step("3. 更优样本 s9@9 迟到 → 产生替换事件",
         eng.add_sample("aux", "s9", 9, seq=3))
    step("4. s9 被撤回 → 回退到次优候选 s8",
         eng.retract_sample("aux", "s9"))
    step("5. 水位线推进到 12 → m1 最终化，之后不再改写",
         eng.advance_watermark(12))
    step("6. 最终化后迟到的 s10@10 被忽略",
         eng.add_sample("aux", "s10", 10, seq=4))
    print(f"  统计: late_samples={eng.late_samples} gc_dropped={eng.gc_dropped}")


if __name__ == "__main__":
    main()
