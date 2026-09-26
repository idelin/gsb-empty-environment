"""Run sample for the temporal partial-order engine (Python 3.11, stdlib only)."""
from fractions import Fraction
from itertools import permutations

from temporal.clocks import Duration, OffsetTable, TimePoint, WallClockTime
from temporal.enumeration import verify
from temporal.network import ContradictionError, TemporalNetwork


def show_net(net, title):
    print(f"== {title} ==")
    print("必然先后关系 necessary-before (含原始约束证据):")
    pairs = net.necessary_pairs()
    if not pairs:
        print("  (none)")
    for (a, b), support in sorted(pairs.items()):
        print(f"  {a} << {b}   evidence: {sorted(support)}")
    print("紧凑表示 transitive reduction (Hasse edges):")
    for a, b in sorted(net.transitive_reduction()):
        print(f"  {a} << {b}")
    print("确定顺序层 layers (同层两两不可比较):")
    for i, layer in enumerate(net.layers()):
        print(f"  L{i}: {list(layer)}")
    print("不可比较组 incomparable groups (antichains, 非等价类):")
    for group in net.incomparable_groups():
        print(f"  {list(group)}")
    print("可行时间见证 feasible witness (exact Fractions):")
    for e, t in sorted(net.witness().items()):
        print(f"  {e} -> {t}")
    print()


def main():
    print("=" * 72)
    print("1. 合成偏移转换表 / 三类时间概念 (wall clock / time point / duration)")
    print("=" * 72)
    table = OffsetTable.synthetic()
    for zone in table.zones():
        print(f"  zone {zone}: offset {table.offset(zone).seconds}s")
    reading_home = WallClockTime("PLUS2", 9 * 3600)
    reading_away = WallClockTime("MINUS5", 2 * 3600)
    tp_home = table.to_timepoint(reading_home)
    tp_away = table.to_timepoint(reading_away)
    print(f"  {reading_home} -> {tp_home}")
    print(f"  {reading_away} -> {tp_away}")
    print(f"  同一绝对时刻? {tp_home == tp_away}")
    gap = table.duration_between(reading_home, WallClockTime("PLUS2", 10 * 3600 + 1))
    print(f"  持续时长 duration_between: {gap} (type={type(gap).__name__})")
    exact = table.to_timepoint(WallClockTime("THIRD", 10))
    print(f"  精确表示: THIRD 10s -> epoch+{exact.offset}s (Fraction, 无浮点误差)")
    print()

    print("=" * 72)
    print("2. 闭区间必然偏序: A=[0,2], B=[3,4], C=[1,3] (不按中点伪造全序)")
    print("=" * 72)
    net = TemporalNetwork()
    net.add_interval("A", 0, 2)
    net.add_interval("B", 3, 4)
    net.add_interval("C", 1, 3)
    show_net(net, "intervals only")
    print(f"  A 必然先于 B? {net.is_necessarily_before('A', 'B')}")
    print(f"  A 与 C 不可比较? {net.incomparable('A', 'C')}  (中点 1<2 也不排序)")
    print(f"  是否全序? {net.is_total()}")
    print()

    print("=" * 72)
    print("3. 显式因果边 + 区间联合推导")
    print("=" * 72)
    net2 = TemporalNetwork()
    net2.add_interval("A", 0, 2)
    net2.add_interval("B", 3, 4)
    net2.add_interval("C", 0, 9)
    net2.add_causal("B", "C")
    show_net(net2, "intervals + causal B->C")
    print()

    print("=" * 72)
    print("4. 矛盾环检测与原始约束证据")
    print("=" * 72)
    bad = TemporalNetwork()
    bad.add_interval("A", 0, 2)
    bad.add_interval("B", 3, 4)
    bad.add_causal("B", "A")  # 与区间隐含的 A<<B 冲突
    print(f"  consistent? {bad.is_consistent()}")
    for cycle in bad.contradictions():
        print(f"  {cycle}")
    try:
        bad.necessary_pairs()
    except ContradictionError as exc:
        print(f"  ContradictionError: {exc}")
    print()

    print("=" * 72)
    print("5. 撤回约束: 失去支持的先后关系被删除, 其余保留")
    print("=" * 72)
    net3 = TemporalNetwork()
    net3.add_interval("A", 0, 2)
    net3.add_interval("B", 3, 4)
    net3.add_interval("C", 5, 6)
    print(f"  撤回前: {sorted(net3.necessary_pairs())}")
    net3.retract("interval:B")
    pairs = net3.necessary_pairs()
    print(f"  撤回 interval:B 后: {sorted(pairs)}")
    print(f"  A<<C 仍成立, 证据: {sorted(pairs[('A', 'C')])}")
    net3.correct_interval("A", 0, 9)
    print(f"  更正 A 为 [0,9] 后: {sorted(net3.necessary_pairs())} (A<<C 失去支持)")
    print()

    print("=" * 72)
    print("6. 输入顺序不影响偏序边")
    print("=" * 72)
    specs = [("A", 0, 2), ("B", 3, 4), ("C", 1, 3), ("D", 0, 1)]
    snapshots = set()
    for perm in permutations(specs):
        net4 = TemporalNetwork()
        for e, lo, hi in perm:
            net4.add_interval(e, lo, hi)
        net4.add_causal("A", "D")
        snapshots.add(tuple(sorted(net4.transitive_reduction())))
    print(f"  24 种输入排列产生的不同紧凑表示数量: {len(snapshots)}")
    print()

    print("=" * 72)
    print("7. 小整数域枚举赋时核验")
    print("=" * 72)
    for title, built, dom in [
        ("intervals A,B,C", net, range(0, 5)),
        ("intervals + causal", net2, range(0, 10)),
        ("after retraction", net3, range(0, 10)),
    ]:
        report = verify(built, dom)
        print(f"  [{title}] {report}")


if __name__ == "__main__":
    main()
