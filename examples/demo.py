"""Runnable demo for the trigger_sets package.

Run from anywhere:  python3.11 examples/demo.py
"""

import sys
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from trigger_sets import (  # noqa: E402
    Checkpoint,
    CheckpointMismatch,
    Duration,
    Engine,
    OffsetTable,
    PeriodicStream,
    TimePoint,
)


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("1. 周期2相位0 与 周期3相位1: 首交点为 4")
    a = PeriodicStream("A", 2, 0)
    b = PeriodicStream("B", 3, 1)
    engine = Engine([a, b])
    print("[0,20) 共同触发:", [str(p.tick) for p in engine.iter_common(0, 20)])
    print("第0个交点:", engine.kth(0, 0, 20).tick, " 计数:", engine.count(0, 20))

    show("2. 版本化暂停 [4,5) 后, 下一交点变为 10")
    a.add_pause(4, 5)
    print("A 规则版本 ->", a.version)
    engine = Engine([a, b])
    print("[0,20) 共同触发:", [str(p.tick) for p in engine.iter_common(0, 20)])
    print("第0个交点:", engine.kth(0, 0, 20).tick)

    show("3. 有理周期 3/2 与 5/4: 统一刻度精确求交")
    c = PeriodicStream("C", Fraction(3, 2), 0)
    d = PeriodicStream("D", Fraction(5, 4), Fraction(1, 2))
    engine = Engine([c, d])
    print("[0,20) 共同触发:", [str(p.tick) for p in engine.iter_common(0, 20)])

    show("4. 例外触发点必须与其他流相交")
    e = PeriodicStream("E", 5, 0)
    f = PeriodicStream("F", 7, 0)
    e.add_exception(3)   # 3 不是 F 的触发点 -> 不加入
    e.add_exception(14)  # 14 是 F 的触发点 -> 加入
    engine = Engine([e, f])
    print("[0,40) 共同触发:", [str(p.tick) for p in engine.iter_common(0, 40)])

    show("5. 阶段切换 (t=5 起相位变为 2)")
    g = PeriodicStream("G", 2, 0)
    g.add_phase_switch(5, 2)
    h = PeriodicStream("H", 3, 1)
    engine = Engine([g, h])
    print("[0,20) 共同触发:", [str(p.tick) for p in engine.iter_common(0, 20)])

    show("6. 无解的算术证据 (周期2相位0 vs 周期2相位1)")
    i = PeriodicStream("I", 2, 0)
    j = PeriodicStream("J", 2, 1)
    engine = Engine([i, j])
    print(engine.explain_no_solution(0, 10))

    show("7. 巨大窗口: 计数与第k个, 不逐tick扫描")
    k = PeriodicStream("K", 10**12 + 39, 0)
    l = PeriodicStream("L", 10**12 + 61, 0)
    engine = Engine([k, l])
    total = engine.count(0, 10**30)
    print("[0, 10^30) 共同触发计数:", total)
    print("第0个:", engine.kth(0, 0, 10**30).tick)
    print("最后一个:", engine.kth(total - 1, 0, 10**30).tick)
    lazy = engine.iter_common(0, 10**45)
    print("惰性产出前两个:", next(lazy).tick, ",", next(lazy).tick)

    show("8. 合成偏移转换表: tick <-> 墙钟时间")
    table = OffsetTable(
        [
            (0, 1_700_000_000, 1),                  # 1 墙钟秒/tick
            (1000, 1_700_001_000, Fraction(1, 2)),  # 之后半速
        ]
    )
    point = TimePoint(4) + Duration(0)
    moment = table.to_wall(point)
    print("TimePoint(4) -> 墙钟秒", moment.seconds, "=", moment.to_datetime())
    print("逆向转换回到:", table.to_timepoint(moment).tick)

    show("9. Checkpoint 绑定所有规则版本")
    m = PeriodicStream("M", 2, 0)
    n = PeriodicStream("N", 3, 1)
    checkpoint = Checkpoint.capture([m, n])
    engine = Engine([m, n], checkpoint=checkpoint)
    print("checkpoint 版本:", checkpoint.versions, " 计数:", engine.count(0, 20))
    m.add_pause(4, 5)
    try:
        engine.count(0, 20)
    except CheckpointMismatch as exc:
        print("规则变更被检出:", exc)


if __name__ == "__main__":
    main()
