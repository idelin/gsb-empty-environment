"""Sample run: priority reservation, edge failure, reroute, recovery."""

from network import Demand, Interval, Network


def show(title, mapping):
    print(title)
    for demand_id, path in mapping.items():
        print(f"  {demand_id}: {'->'.join(path) if path is not None else 'FAILED'}")


def main() -> None:
    net = Network()
    net.add_vertex("E")  # isolated vertex, preserved in the topology
    net.add_edge("e1", "A", "B", 10)
    net.add_edge("e2", "B", "C", 10)
    net.add_edge("e3", "A", "C", 4)
    net.add_edge("e4", "A", "B", 6)  # parallel multi-edge with e1
    net.add_edge("e5", "C", "D", 8)

    demands = [
        Demand("d1", priority=1, source="A", target="C", bandwidth=6,
               interval=Interval(0, 10)),
        Demand("d2", priority=2, source="A", target="C", bandwidth=5,
               interval=Interval(0, 10)),
        Demand("d3", priority=2, source="A", target="C", bandwidth=5,
               interval=Interval(5, 15)),
        Demand("d4", priority=3, source="C", target="D", bandwidth=8,
               interval=Interval(0, 10)),
    ]
    by_id = {d.id: d for d in demands}

    print("== initial reservation (priority desc, id asc) ==")
    show("results:", net.reserve_all(demands))
    print(f"isolated vertex kept: {'E' in net.vertices}")

    print("\n== fail edge e1 (only affected demands reroute) ==")
    show("reroute:", net.fail_edge("e1", by_id))
    print(f"d4 placement untouched: {net.placement('d4')}")

    print("\n== recover e1 (idempotent, no double capacity) ==")
    interval = Interval(0, 10)
    net.recover_edge("e1")
    first = net.available_capacity("e1", interval)
    net.recover_edge("e1")  # no-op
    second = net.available_capacity("e1", interval)
    print(f"e1 available after recover: {first}, after 2nd recover: {second}")


if __name__ == "__main__":
    main()
