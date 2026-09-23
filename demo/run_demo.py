"""运行本地夹具，演示键连接、撤回、水位清理与迟到报告。

用法：
    python3.11 demo/run_demo.py
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from event_join import Change, ToleranceJoin, Watermark  # noqa: E402

from demo.fixtures import (  # noqa: E402
    EVENTS,
    KEY_COL,
    ORDERS,
    SHIPMENTS,
    TOLERANCE,
    TS_COL,
)


def describe(item) -> str:
    if isinstance(item, Watermark):
        return f"WATERMARK  ts={item.ts}  (seq={item.seq})"
    assert isinstance(item, Change)
    if item.before is None:
        action = f"+{item.after}"
    elif item.after is None:
        action = f"-{item.before}"
    else:
        action = f"{item.before} -> {item.after}"
    return f"{item.op.value:<6} {item.table:<9} pk={item.pk:<7} {action}"


def main() -> None:
    join = ToleranceJoin(
        left_table=ORDERS,
        right_table=SHIPMENTS,
        key_col=KEY_COL,
        ts_col=TS_COL,
        tolerance=TOLERANCE,
    )

    print("=" * 72)
    print("事件时间容差键连接演示  (左=orders 右=shipments 容差=5 闭区间)")
    print("=" * 72)
    for item, note in EVENTS:
        join.ingest(item)
        produced = join.output()
        tail = ""
        if produced and isinstance(item, Change):
            recent = [
                f"{e.sign}{e.pair_id}"
                for e in produced
                if e.seq == item.seq
            ]
            if recent:
                tail = "  => 输出: " + ", ".join(recent)
        print(f"[seq={item.seq:>2}] {describe(item)}")
        print(f"         # {note}{tail}")

    print("=" * 72)
    print("配对 changelog（去重后的 + 建立 / - 撤回）")
    print("-" * 72)
    for e in join.output():
        print(f"  {e.sign} {e.pair_id:<12} key={e.key}  "
              f"左ts={e.left_ts} 右ts={e.right_ts} (seq={e.seq})")

    print("-" * 72)
    print(f"当前存活配对 ID（去重） : {join.live_pairs()}")
    print(f"已最终化配对 ID         : {join.finalized_pairs()}")
    print(f"最终水位                : {join.watermark}")

    print("-" * 72)
    print("迟到 / 越界旁路报告（未静默丢弃）")
    if not join.late:
        print("  （无）")
    for r in join.late:
        print(f"  seq={r.seq:<2} {r.reason:<13} {r.op.value:<6} "
              f"{r.table}.{r.pk} key={r.key} event_ts={r.event_ts} "
              f"wm={r.watermark}  # {r.detail}")
    print("=" * 72)


if __name__ == "__main__":
    main()
