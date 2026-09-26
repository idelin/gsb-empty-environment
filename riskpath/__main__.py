"""Runnable demo: ``python -m riskpath``."""

from fractions import Fraction

from . import Graph, Status, run_search


def fmt(label):
    return " -> ".join(str(v) for v in label.path)


def demo_main_graph():
    g = Graph()
    # "weather" is a shared failure event: both edges fail together,
    # so path s-a-t gets one factor 0.9, not 0.9 * 0.9.
    g.add_edge("s", "a", cost=2, delay=3, success=0.9, group="weather")
    g.add_edge("a", "t", cost=2, delay=2, success=0.9, group="weather")
    g.add_edge("s", "b", cost=1, delay=4, success=0.95)          # private group
    g.add_edge("b", "t", cost=3, delay=1, success=0.9)           # private group
    g.add_edge("s", "c", cost=3, delay=1, success=0.99)          # private group
    g.add_edge("c", "t", cost=1, delay=3, success=0.99)          # private group
    g.add_edge("a", "b", cost=1, delay=1, success=1.0)
    g.add_edge("b", "c", cost=1, delay=1, success=1.0)
    g.add_edge("c", "a", cost=1, delay=1, success=1.0)          # zero-risk cycle
    g.add_edge("s", "t", cost=2, delay=6, success=0.80)          # cheap but slow
    return g


def main() -> None:
    print("=== Demo 1: dual budgets, independent + shared risk groups ===")
    g = demo_main_graph()
    res = run_search(g, "s", "t", cost_budget=6, delay_budget=7, mode="frontier")
    print(f"status: {res.status.value}, label expansions: {res.expansions}")
    print("Pareto frontier (cost, delay, success):")
    for label in res.frontier:
        print(f"  cost={label.cost} delay={label.delay} "
              f"success={label.success:.4f}  {fmt(label)}")
    print(f"optimal within budgets: success={res.best.success:.4f}  {fmt(res.best)}")

    print()
    print("=== Demo 2: multiplication, not addition ===")
    g2 = Graph()
    g2.add_edge("s", "a", success=0.9)
    g2.add_edge("a", "t", success=0.9)
    r2 = run_search(g2, "s", "t", cost_budget=10, delay_budget=10)
    print(f"two independent 0.9 edges -> {r2.best.success:.4f} (0.9 * 0.9 = 0.81)")

    print()
    print("=== Demo 3: shared risk group counted once ===")
    g3 = Graph()
    g3.add_edge("s", "a", success=0.9, group="G")
    g3.add_edge("a", "t", success=0.9, group="G")
    r3 = run_search(g3, "s", "t", cost_budget=10, delay_budget=10)
    print(f"two edges in one shared group G -> {r3.best.success:.4f} "
          "(common failure event, counted once)")

    print()
    print("=== Demo 4: undecided -> resumable -> optimal; relaxed-budget resume ===")
    g4 = Graph()
    g4.add_edge("s", "t", cost=2, delay=1, success=0.9)
    tight = run_search(g4, "s", "t", cost_budget=Fraction(1), delay_budget=10,
                       max_expansions=100)
    print(f"cost budget 1: {tight.status.value}")
    resumed = run_search(g4, "s", "t", cost_budget=2, delay_budget=10,
                         state=tight.state)
    print(f"resume with relaxed cost budget 2: {resumed.status.value}, "
          f"success={resumed.best.success:.4f}")
    stopped = run_search(g, "s", "t", cost_budget=6, delay_budget=7,
                         max_expansions=1)
    print(f"expansion cap 1: {stopped.status.value}")
    cont = run_search(g, "s", "t", cost_budget=6, delay_budget=7,
                      mode="frontier", max_expansions=1000, state=stopped.state)
    print(f"resumed to completion: {cont.status.value}, "
          f"frontier size {len(cont.frontier)}")


if __name__ == "__main__":
    main()
