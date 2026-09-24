"""运行样例：预留 -> 边故障重路由 -> 边恢复。"""

from network_reservation import Demand, Graph, Interval, ReservationManager


def show_reservations(manager: ReservationManager, title: str) -> None:
    print(f"--- {title} ---")
    for demand_id, r in sorted(manager._reservations.items()):
        path = " -> ".join(r.path) if r.path else "(空路径)"
        print(f"  {demand_id}: 路径 [{path}]")


def main() -> None:
    graph = Graph()
    # 拓扑：A -e1- B -e2- C，另有备选 A -e3- D -e4- C；e5 是 A-B 的多重边；Z 是孤立点
    graph.add_edge("e1", "A", "B", capacity=10)
    graph.add_edge("e2", "B", "C", capacity=10)
    graph.add_edge("e3", "A", "D", capacity=5)
    graph.add_edge("e4", "D", "C", capacity=5)
    graph.add_edge("e5", "A", "B", capacity=4)
    graph.add_vertex("Z")  # 孤立点保留

    manager = ReservationManager(graph)

    demands = [
        Demand("d-low", "A", "C", bandwidth=6, interval=Interval(0, 10), priority=1),
        Demand("d-high", "A", "C", bandwidth=6, interval=Interval(0, 10), priority=9),
        Demand("d-mid-a", "A", "B", bandwidth=4, interval=Interval(0, 10), priority=5),
        Demand("d-mid-b", "A", "B", bandwidth=4, interval=Interval(0, 10), priority=5),
        # 与 d-high 首尾相接 [10, 20)：左闭右开，不冲突
        Demand("d-next", "A", "C", bandwidth=6, interval=Interval(10, 20), priority=9),
    ]

    results = manager.reserve_all(demands)
    print("=== 按 (优先级, 需求ID) 顺序预留 ===")
    for demand_id, ok in results.items():
        print(f"  {demand_id}: {'成功' if ok else '失败'}")

    show_reservations(manager, "预留结果")
    # d-high 先占 e1+e2（容量10用6）；d-low 只能走 e3+e4（容量5不足6）-> 失败
    # d-mid-a 占 e5（容量4），d-mid-b 占 e1 剩余4 -> 同优先级按 ID 顺序

    print()
    print("=== 故障边 e1（仅重路由受影响需求）===")
    outcome = manager.fail_edge("e1")
    print(f"  重路由成功: {outcome['rerouted']}")
    print(f"  回滚(无路可绕): {outcome['rolled_back']}")
    show_reservations(manager, "故障后")

    print()
    print("=== 恢复边 e1（重复恢复为空操作）===")
    print(f"  第一次恢复: {manager.recover_edge('e1')}")
    print(f"  第二次恢复: {manager.recover_edge('e1')}")
    print(f"  e1 当前可用容量(区间[0,10)): {manager.available_capacity('e1', Interval(0, 10))}")


if __name__ == "__main__":
    main()
