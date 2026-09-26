from fractions import Fraction

from bounded_mincost_flow import Edge, FlowProblem, Status


def show(title, result):
    print(f"== {title} ==")
    print("status:", result.status.value)
    print("flow:", result.flow)
    print("cost:", result.cost)
    print("cycles_canceled:", result.cycles_canceled)
    if result.cycle is not None:
        print("remaining_negative_cycle:", " -> ".join(map(str, result.cycle)))
    if result.evidence is not None:
        evidence = result.evidence
        print("evidence:", evidence.kind, "-", evidence.message)
        if evidence.kind == "cut_certificate":
            cut = evidence.details["cut"]
            print("reachable:", cut.reachable)
            print("net_outflow_bound:", cut.upper_bound_on_net_outflow)
            print("required:", cut.required_net_outflow)
            print("deficit:", cut.deficit)
    print()


optimal = FlowProblem(
    [
        Edge("s", "a", 0, 2, 1, "cheap-in"),
        Edge("s", "b", 0, 2, 3, "other-in"),
        Edge("a", "t", 0, 2, 1, "cheap-out"),
        Edge("b", "t", 0, 2, 1, "other-out"),
    ],
    {"s": 2, "t": -2},
).solve()
show("最优整数流", optimal)

negative_cycle = FlowProblem(
    [
        Edge("s", "t", 0, 1, 10, "shipment"),
        Edge("a", "b", 0, 2, Fraction(-10), "discount"),
        Edge("b", "a", 0, 2, 6, "return"),
    ],
    {"s": 1, "t": -1},
).solve()
show("可达负费用循环", negative_cycle)

budget = FlowProblem(
    [Edge("a", "a", 0, 2, -3, "negative-self-loop")],
    {"a": 0},
).solve(max_cycles=0)
show("预算未决", budget)

infeasible = FlowProblem(
    [Edge("s", "t", 0, 1, 0, "narrow-edge")],
    {"s": 2, "t": -2},
).solve()
show("不可行证据", infeasible)
