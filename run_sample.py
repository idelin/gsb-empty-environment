"""合成数据运行样例：等价类敏感值分布 vs 总体的增量监控。

演示：TV（离散敏感值）、有序一维输运（数值敏感值）、一般成本矩阵、
数据撤回后的版本失效与重新评估、阈值证据、空类处理、输运流验证。

注意：本样例仅使用合成数据；统计实验结果不构成任何形式化隐私证明。
"""

from fractions import Fraction

from distances import general_transport, ordered_transport, verify_transport_flow
from monitor import DistributionMonitor


def show(ev):
    dist = "undefined(empty)" if ev.distance is None else str(ev.distance)
    print(f"  类 {ev.class_id!r}: 基数={ev.class_size}/{ev.population_size} "
          f"距离={dist} 阈值={ev.threshold} 达标={ev.within_threshold} 余量={ev.margin}")


print("=== 1. 离散敏感值（诊断类别），总变差 TV，阈值 1/4 ===")
mon = DistributionMonitor(metric="tv", threshold=Fraction(1, 4))
records = [
    ("age20_zip100", "flu"), ("age20_zip100", "flu"), ("age20_zip100", "cold"),
    ("age30_zip200", "flu"), ("age30_zip200", "cold"), ("age30_zip200", "cold"),
    ("age40_zip300", "hiv"), ("age40_zip300", "hiv"), ("age40_zip300", "hiv"),
]
for cid, v in records:
    mon.add(cid, v)
for ev in mon.evaluate_all().values():
    show(ev)
ev = mon.evaluation("age20_zip100")
print("  逐桶贡献 (值: 类计数/总体计数 |p-q|/2):")
for c in ev.contributions:
    print(f"    {c.value}: {c.class_count}/{c.population_count} -> {c.contribution}")

print("\n=== 2. 数据撤回：总体基线版本变化，所有类需重新评估 ===")
print(f"  撤回前总体版本: {mon.population_version}")
mon.remove("age40_zip300", "hiv")
mon.remove("age40_zip300", "hiv")
print(f"  撤回 2 条后总体版本: {mon.population_version}, 过期类: {mon.stale_classes()}")
for ev in mon.evaluate_all().values():
    show(ev)

print("\n=== 3. 空等价类：不除零，距离未定义 ===")
mon.remove("age40_zip300", "hiv")
show(mon.evaluate("age40_zip300"))

print("\n=== 4. 有序敏感值（收入档），一维输运距离 W1 ===")
mon2 = DistributionMonitor(metric="ordered", ordered_values=[1, 2, 3, 4, 5],
                           threshold=Fraction(1, 2))
for cid, v in [("g1", 1), ("g1", 2), ("g1", 2), ("g2", 4), ("g2", 5), ("g2", 5)]:
    mon2.add(cid, v)
for ev in mon2.evaluate_all().values():
    show(ev)
ev = mon2.evaluation("g1")
print("  逐区间贡献 (区间: 累计差 * 长度 = 贡献):")
for it in ev.contributions:
    print(f"    [{it.left},{it.right}]: {it.cum_diff} * {it.gap} = {it.contribution}")
print("  最优输运流:")
for e in ev.flow:
    print(f"    {e.source} -> {e.target}: {e.amount}")
p, q = mon2._classes["g1"], mon2.population
res = ordered_transport(p, q, [1, 2, 3, 4, 5])
check = verify_transport_flow(p, q, res.flow, lambda a, b: abs(a - b),
                              expected_cost=res.distance)
print(f"  验证器: 可行={check.feasible} 成本={check.cost} 最优={check.optimal}")

print("\n=== 5. 一般成本矩阵：真正的最小费用流（非一维累计公式） ===")
costs = {("x", "x"): 0, ("x", "y"): 2, ("y", "x"): 5, ("y", "y"): 1}
mon3 = DistributionMonitor(metric="general", cost_matrix=costs)
for cid, v in [("h1", "x"), ("h1", "x"), ("h1", "y"), ("h2", "y"), ("h2", "y"), ("h2", "y")]:
    mon3.add(cid, v)
ev = mon3.evaluate("h1")
show(ev)
p, q = mon3._classes["h1"], mon3.population
res = general_transport(p, q, cost_matrix=costs)
check = verify_transport_flow(p, q, res.flow, lambda a, b: costs[(a, b)],
                              expected_cost=res.distance)
print(f"  验证器: 可行={check.feasible} 成本={check.cost} 最优={check.optimal}")
