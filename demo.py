"""运行样例：python3.11 demo.py"""

from __future__ import annotations

from datetime import timezone

from temporal_po import (
    Duration,
    TimePoint,
    TemporalOrder,
    UnitEntry,
    UnitTable,
    WallClock,
    render,
)


def show(title: str, order: TemporalOrder) -> None:
    print("=" * 72)
    print(title)
    print("-" * 72)
    print(render(order.analyze()))
    print()


# 样例 1：闭区间相接不排序；明确相离才必然先后
ex1 = TemporalOrder(default_domain=(0, 10))
ex1.add_interval("iA", "A", 0, 2)
ex1.add_interval("iB", "B", 3, 4)
ex1.add_interval("iC", "C", 2, 3)  # 与 A 在 2 处相接：只保证 ≤，不保证 <
show("样例1：A=[0,2] 必然先于 B=[3,4]；A 与 C=[2,3] 在时刻2相接 → 不可比较", ex1)

# 样例 2：输入顺序打乱，结果不变
ex2 = TemporalOrder(default_domain=(0, 10))
ex2.add_causal("e2", "X", "Z", 1)
ex2.add_interval("iZ", "Z", 5, 8)
ex2.add_causal("e1", "W", "X", 2)
ex2.add_interval("iW", "W", 0, 3)
show("样例2：约束乱序录入（e2 先于 e1）——层、边、证据均与顺序无关", ex2)

# 样例 3：撤回一条约束，失去支持的传递关系被删除
ex3 = TemporalOrder(default_domain=(0, 12))
ex3.add_interval("iA", "A", 0, 2)
ex3.add_interval("iB", "B", 0, 12)
ex3.add_causal("eAB", "A", "B", 1)
a3 = ex3.analyze()
print("=" * 72)
print("样例3：撤回约束 eAB（A≺B 的唯一来源）")
print("-" * 72)
print("撤回前必然关系:", [(r.earlier, r.later) for r in a3.closure])
ex3.retract("eAB")
a3b = ex3.analyze()
print("撤回后必然关系:", [(r.earlier, r.later) for r in a3b.closure])
print("撤回后不可比较:", a3b.incomparable)
print()

# 样例 4：更正区间后矛盾消除
ex4 = TemporalOrder(default_domain=(0, 10))
ex4.add_interval("iP", "P", 5, 9)
ex4.add_interval("iQ", "Q", 0, 2)
ex4.add_causal("ePQ", "P", "Q", 1)
print("=" * 72)
print("样例4：P∈[5,9]、Q∈[0,2] 却有因果边 P≺Q → 矛盾环")
print("-" * 72)
print(render(ex4.analyze()))
ex4.correct_interval("iP", low=0, high=1)
print("\n更正 iP 为 P∈[0,1] 后：")
print(render(ex4.analyze()))
print()

# 样例 5：纯因果矛盾环
ex5 = TemporalOrder(default_domain=(0, 20))
for e in ("R", "S", "T"):
    ex5.add_event(e)
ex5.add_causal("g1", "R", "S", 1)
ex5.add_causal("g2", "S", "T", 1)
ex5.add_causal("g3", "T", "R", 2)
show("样例5：显式因果边形成正权环 R→S→T→R（间隔合计 4>0）", ex5)

# 样例 6：精确时间表示——合成偏移转换表、墙钟/时间点/时长
table = UnitTable(
    {
        "tick": UnitEntry("tick", 1),
        "second": UnitEntry("second", 1),
        "minute": UnitEntry("minute", 60),
        "hour": UnitEntry("hour", 3600),
        # 合成单位：每拍 30 秒，第 0 拍从 90 秒（1分30秒）开始
        "beat": UnitEntry("beat", 30, offset_ticks=90),
    }
)
from datetime import datetime

epoch = WallClock(datetime(2026, 9, 26, 0, 0, 0, tzinfo=timezone.utc))
p0 = TimePoint(table.to_ticks(3, "beat"))   # 90 + 3*30 = 180s
p1 = TimePoint(table.to_ticks(5, "beat"))   # 90 + 5*30 = 240s
delta: Duration = p1 - p0
wc0 = WallClock.from_timepoint(p0, epoch)
wc1 = WallClock.from_timepoint(p1, epoch)
print("=" * 72)
print("样例6：墙钟时间 / 时间点 / 持续时长 的精确区分")
print("-" * 72)
print(f"第3拍 = {p0.ticks} ticks = 墙钟 {wc0}")
print(f"第5拍 = {p1.ticks} ticks = 墙钟 {wc1}")
print(f"持续时长 delta = {delta.ticks} ticks = {delta.format(table)}")
print(f"点坐标反算: p1={table.from_ticks(p1.ticks, 'beat')} 拍, p0={table.from_ticks(p0.ticks, 'beat')} 拍（偏移90s 是点坐标的基准）")
print(f"时长只按零偏移单位换算: {delta.ticks} ticks = {delta.format(table)}，不拿带偏移的“拍”度量时长")
print(f"类型不可混: TimePoint+TimePoint 未定义；点-点=Duration，点+Duration=TimePoint")
try:
    _ = p0 + p1  # type: ignore[operator]
except TypeError:
    print("已验证: TimePoint + TimePoint 抛出 TypeError")
