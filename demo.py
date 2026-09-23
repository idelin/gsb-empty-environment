"""Sample run: python3 demo.py"""

from pareto_paths import Graph, pareto_paths


def show(title, result):
    status = "COMPLETE" if result.is_complete else "TRUNCATED (partial, not complete)"
    print(f"{title} [budget={result.budget:g}] -> {status}")
    if not result.paths:
        print("  (empty set: no feasible path)")
    for p in result.paths:
        route = " -> ".join(p.vertices)
        print(f"  cost={p.cost:g} delay={p.delay:g}  vertices: {route}  edges: {p.edges}")
    print()


def main():
    graph = Graph()
    graph.add_vertex("lonely")  # isolated vertex, kept in the graph
    graph.add_edge("e1", "S", "A", cost=2, delay=4)
    graph.add_edge("e2", "S", "B", cost=4, delay=1)
    graph.add_edge("e3", "A", "T", cost=2, delay=5)
    graph.add_edge("e4", "A", "B", cost=1, delay=1)
    graph.add_edge("e5", "B", "T", cost=1, delay=1)
    graph.add_edge("e6", "B", "T", cost=0, delay=6)  # multi-edge B -> T
    print(f"vertices (isolated kept): {sorted(graph.vertices)}")
    print(f"edges (multi-edges kept): {[e.id for e in graph.edges]}")
    print()

    show("full frontier", pareto_paths(graph, "S", "T", budget=5))
    show("capped at 2 results", pareto_paths(graph, "S", "T", budget=5, max_results=2))
    show("budget too small", pareto_paths(graph, "S", "T", budget=2))


if __name__ == "__main__":
    main()
