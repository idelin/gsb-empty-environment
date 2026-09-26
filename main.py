"""运行样例：加权最大最小公平分配、证书、增量再分配、网格核验。"""

from fractions import Fraction

from wmmf import (
    Demand,
    FairNetwork,
    build_certificate,
    explain,
    verify_all,
    water_fill,
)


def show_allocation(title, alloc):
    print(f"== {title} ==")
    for name in sorted(alloc.rates):
        cap = alloc.demands[name].cap
        state = "已达上限" if alloc.is_satisfied(name) else "未满足"
        print(
            f"  需求 {name}: 速率 {alloc.rates[name]} "
            f"(归一 {alloc.normalized_rate(name)}, 上限 {cap}, {state})"
        )
    for link in sorted(alloc.link_loads):
        print(f"  链路 {link}: 负载 {alloc.link_loads[link]} / 容量 {alloc.link_caps[link]}")
    print("  冻结顺序:")
    for event in alloc.events:
        print(
            f"    第 {event.round_index} 轮 @ 水位 {event.level}: "
            f"冻结 {list(event.demands)}"
            + (f", 瓶颈链路 {list(event.bottleneck_links)}" if event.bottleneck_links else "")
            + (f", 因达上限冻结 {list(event.capped)}" if event.capped else "")
        )
    print(f"  瓶颈链路: {alloc.bottleneck_links()}")


def show_verification(alloc, grid_step=None, grid_budget=100_000):
    report = verify_all(alloc, grid_step=grid_step, grid_budget=grid_budget)
    print(f"  可行: {'是' if report.feasible else '否 ' + str(report.feasible_errors)}")
    print(f"  最优(证书): {'是' if report.optimal else '否 ' + str(report.certificate_errors)}")
    if report.grid is not None:
        print(
            f"  网格穷举: {report.grid.status.value} "
            f"(检查 {report.grid.checked} 个网格点)"
            + (f", 反例 {report.grid.counterexample}" if report.grid.counterexample else "")
        )
    return report


def demo_basic():
    links = {"A": 10, "B": 4}
    demands = {
        "d1": Demand("d1", weight=1, cap=8, links=("A",)),
        "d2": Demand("d2", weight=1, cap=6, links=("A", "B")),  # 多链路共享
        "d3": Demand("d3", weight=2, cap=3, links=("A",)),
    }
    alloc = water_fill(links, demands)
    show_allocation("基本加权分配（d2 多链路共享，速率不重复计算）", alloc)
    cert = build_certificate(alloc)
    print("  公平证书:")
    for name in sorted(alloc.rates):
        print(f"    {explain(alloc, cert, name)}")
    show_verification(alloc, grid_step=Fraction(1, 2))
    print()


def demo_incremental():
    net = FairNetwork()
    net.add_link("A", 10)
    net.add_link("B", 6)
    net.add_demand(Demand("d1", weight=1, cap=10, links=("A",)))
    net.add_demand(Demand("d2", weight=1, cap=10, links=("A", "B")))
    alloc = net.allocate()
    show_allocation("初始分配", alloc)

    print("-- 撤回需求 d1，触发增量再分配（解冻 d2，而非只扣剩余量）--")
    net.withdraw_demand("d1")
    report = net.reallocate()
    show_allocation("再分配结果", report.current)
    print(f"  饱和集合: {sorted(report.saturated_before)} -> {sorted(report.saturated_after)}")
    print(f"  被解冻提速的需求: {list(report.unfrozen)}")
    for name, (old, new) in sorted(report.increased.items()):
        print(f"    {name}: {old} -> {new}")
    show_verification(report.current, grid_step=1)

    print("-- 链路 B 故障（容量 6 -> 0）--")
    net.fail_link("B")
    report = net.reallocate()
    show_allocation("故障后再分配", report.current)
    print(f"  饱和集合: {sorted(report.saturated_before)} -> {sorted(report.saturated_after)}")
    show_verification(report.current, grid_step=1)
    print()


def demo_budget_undecided():
    links = {"A": 10}
    demands = {
        "d1": Demand("d1", weight=1, cap=10, links=("A",)),
        "d2": Demand("d2", weight=1, cap=10, links=("A",)),
    }
    alloc = water_fill(links, demands)
    show_allocation("预算未决演示（网格 11x11，预算 10）", alloc)
    show_verification(alloc, grid_step=1, grid_budget=10)


if __name__ == "__main__":
    demo_basic()
    demo_incremental()
    demo_budget_undecided()
