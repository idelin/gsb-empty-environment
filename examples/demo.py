"""运行样例：优先级区间叠加时间轴 + 合成偏移转换表。

运行：python3.11 examples/demo.py
"""

from datetime import datetime, timezone
from fractions import Fraction
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from priority_timeline import (  # noqa: E402
    ConflictError, Duration, OffsetTable, TimePoint, Timeline, WallClock,
)

UTC = timezone.utc


def wc(hour, minute=0):
    return WallClock(datetime(2026, 9, 25, hour, minute, tzinfo=UTC))


def show(title, tl, table):
    print(f"\n== {title} ==")
    for seg in tl.segments():
        span = f"[{seg.start.offset}, {seg.end.offset})"
        clock = f"{table.wallclock_at(seg.start).at:%H:%M}~{table.wallclock_at(seg.end).at:%H:%M}"
        if seg.is_conflict:
            candidates = ", ".join(
                f"{v!r}({s})" for v, s in zip(seg.values, seg.sources)
            )
            print(f"  {span:12} {clock}  冲突段 p{seg.priority}: {candidates}")
        else:
            print(f"  {span:12} {clock}  p{seg.priority} {seg.value!r}  来源={seg.sources}")


def main():
    # 合成偏移转换表：tick 0->08:00, tick 100->10:00, tick 200->10:30（非均匀）
    table = OffsetTable([(0, wc(8)), (100, wc(10)), (200, wc(10, 30))])

    print("== 三种时间概念 ==")
    tp = TimePoint(Fraction(1, 3))          # 时间点：合成轴精确偏移 1/3 tick
    wall = table.wallclock_at(tp)           # 墙钟时间：经转换表映射
    dur = table.duration_between(TimePoint(0), tp)  # 持续时长：精确 24 秒
    print(f"  TimePoint: {tp!r}  (Fraction 精确偏移)")
    print(f"  WallClock: {wall!r}")
    print(f"  Duration : {dur!r}  (1/3 tick * 72s/tick = 24s，无浮点误差)")

    tl = Timeline()

    # 1) 高优先 [3,7) 覆盖低优先 [0,10) 仅中段
    tl.add(0, 10, "低优先", priority=0, source="基础表")
    high = tl.add(3, 7, "高优先", priority=1, source="临时调度")
    show("高优先 [3,7) 覆盖低优先 [0,10) 仅中段", tl, table)

    # 2) 撤回高优先，恢复底层
    tl.remove(high)
    show("撤回高优先后，底层恢复并重新合并", tl, table)

    # 3) 同级冲突显式保留
    tl2 = Timeline()
    tl2.add(0, 10, "方案A", priority=1, source="传感器-甲")
    tl2.add(3, 7, "方案B", priority=1, source="传感器-乙")
    show("同级冲突显式保留（不静默取舍）", tl2, table)
    conflict = tl2.conflicts()[0]
    try:
        conflict.value
    except ConflictError as exc:
        print(f"  取单值被拒绝: {exc}")

    # 4) 相邻同值不同来源：证据不丢
    tl3 = Timeline()
    tl3.add(0, 5, "运行", source="日志-1")
    tl3.add(5, 10, "运行", source="日志-2")
    show("相邻同值不同来源：保留两段证据", tl3, table)
    tl3.add(10, 15, "运行", source="日志-2")  # 与上一段同值同来源 -> 合并
    show("同值同来源相邻则合并（条目仍各自留痕）", tl3, table)
    merged = tl3.segments()[-1]
    print(f"  合并段 entries: {[(e.id, e.source, str(e.start.offset), str(e.end.offset)) for e in merged.entries]}")

    # 5) 局部更新：只影响重叠窗口
    tl4 = Timeline()
    tl4.add(0, 20, "底", priority=0, source="base")
    tl4.add(15, 18, "远", priority=1, source="far")
    far_before = tl4.segment_at(16)
    h = tl4.add(3, 7, "近", priority=1, source="near")
    tl4.remove(h)
    print("\n== 局部更新 ==")
    print(f"  增删 [3,7) 后，远处分段对象不变: {tl4.segment_at(16) is far_before}")


if __name__ == "__main__":
    main()
