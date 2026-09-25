"""trigsync 可运行样例：python3.11 examples/demo.py"""

import sys
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from trigsync import (
    Duration,
    ExceptionRule,
    Instant,
    Intersection,
    OffsetEntry,
    OffsetTable,
    PauseRule,
    PhaseRule,
    Stream,
)


def show(title):
    print(f"\n=== {title} ===")


# 场景 1：周期2相位0 与 周期3相位1 —— 首交 4；排除 [4,5) 后下一交点为 10
show("场景1: 首交与暂停")
a = Stream("A", (PhaseRule(1, 0, 2, 0),))
b = Stream("B", (PhaseRule(1, 0, 3, 1),))
eng = Intersection([a, b], 0, 30)
print("共同交点:", [str(p.value) for p in eng.iter_points()])
print("首交:", eng.first().value)
a_paused = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(1, 4, 5),))
eng = Intersection([a_paused, b], 0, 30)
print("排除 [4,5) 后:", [str(p.value) for p in eng.iter_points()])

# 场景 2：例外点必须与其他流相交，非共同例外不可加入
show("场景2: 例外点")
a = Stream("A", (PhaseRule(1, 0, 2, 0),),
           exceptions=(ExceptionRule(1, 7), ExceptionRule(1, 8), ExceptionRule(1, 15)))
b = Stream("B", (PhaseRule(1, 0, 3, 1),),
           exceptions=(ExceptionRule(1, 9), ExceptionRule(1, 15)))
eng = Intersection([a, b], 0, 30)
print("共同交点:", [str(p.value) for p in eng.iter_points()])
print("  7: B 同余命中 -> 加入；8/9: 单边例外 -> 拒绝；15: 双方例外 -> 加入")

# 场景 3：无解的算术证据
show("场景3: 无解证据")
a = Stream("A", (PhaseRule(1, 0, 2, 0),))
b = Stream("B", (PhaseRule(1, 0, 2, 1),))
eng = Intersection([a, b], 0, 10)
print("交点数:", eng.count())
for report in eng.reports:
    print("证据:", report)

# 场景 4：有理周期，统一刻度精确表示
show("场景4: 有理周期")
a = Stream("A", (PhaseRule(1, 0, Fraction(3, 2), Fraction(1, 2)),))
b = Stream("B", (PhaseRule(1, 0, Fraction(5, 4), 0),))
eng = Intersection([a, b], 0, 20)
print("统一刻度: 1/%d" % eng.scale.denominator)
print("共同交点:", [str(p.value) for p in eng.iter_points()])

# 场景 5：巨大窗口，整式计数 + 第 k 个交点，不逐 tick 扫描
show("场景5: 巨大窗口")
a = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(1, 10, 4 * 10**14),))
b = Stream("B", (PhaseRule(1, 0, 3, 1),))
eng = Intersection([a, b], 0, 10**15)
print("窗口 [0, 10^15) 内交点总数:", eng.count())
print("第 0 个:", eng.kth(0).value, " 第 1 个:", eng.kth(1).value)
print("最后一个:", eng.kth(eng.count() - 1).value)

# 场景 6：相位切换（版本化规则）
show("场景6: 相位切换")
a = Stream("A", (PhaseRule(1, 0, 2, 0), PhaseRule(2, 10, 2, 1)))
b = Stream("B", (PhaseRule(1, 0, 4, 0),))
eng = Intersection([a, b], 0, 20)
print("共同交点:", [str(p.value) for p in eng.iter_points()])

# 场景 7：墙钟转换（合成偏移转换表）与 checkpoint
show("场景7: 墙钟与 checkpoint")
table = OffsetTable([OffsetEntry(0, 1_700_000_000, 1), OffsetEntry(100, 1_700_000_100, 2)])
wall = table.to_wall(Instant(150))
print("Instant(150) ->", wall, " 往返:", table.to_instant(wall))
print("Instant(150) + Duration(5) =", Instant(150) + Duration(5))
a = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(1, 4, 5),))
eng = Intersection([a, Stream("B", (PhaseRule(1, 0, 3, 1),))], 0, 30)
print("checkpoint（绑定全部规则版本）:", eng.checkpoint.digest()[:16], "...")
