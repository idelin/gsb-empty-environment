"""End-to-end demo: top-K legal paths in the vertex x state product space.

Run from the repository root:  python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from product_paths import (  # noqa: E402
    CursorVersionError,
    Edge,
    ForbiddenRule,
    Graph,
    RuleSet,
    brute_force_paths,
    enumerate_paths,
    verify_enumeration,
)


def show(title, result):
    print(f"== {title} ==")
    for rank, path in enumerate(result.paths, 1):
        trajectory = " -> ".join(
            f"{vertex}[{state}]" for vertex, state in path.state_trajectory()
        )
        print(
            f"  {rank}. cost={path.cost:.0f} edges={path.edge_count} "
            f"ids={path.edge_ids}"
        )
        print(f"     trajectory: {trajectory}")
    print(
        f"  complete={result.complete} expansions={result.stats.expansions} "
        f"generated={result.stats.generated} "
        f"pruned_forbidden={result.stats.pruned_forbidden} "
        f"pruned_visited={result.stats.pruned_visited}"
    )
    if result.stats.pending_states:
        print(f"  pending_states={result.stats.pending_states}")
    print()


def build_graph():
    graph = Graph()
    graph.add_vertex("E")  # isolated vertex, kept in the model
    graph.add_edge(Edge("e1", "A", "B", 1.0, {"clear": "loaded"}))
    graph.add_edge(Edge("e2", "A", "B", 1.0, {"clear": "clear"}))  # parallel edge
    graph.add_edge(Edge("e3", "B", "D", 1.0, {"loaded": "clear", "clear": "clear"}))
    graph.add_edge(Edge("e4", "A", "C", 2.0, {"clear": "clear"}))
    graph.add_edge(Edge("e5", "C", "D", 0.0, {"clear": "clear"}))
    graph.add_edge(Edge("e6", "C", "B", 0.0, {"clear": "clear"}))  # zero-weight
    graph.add_edge(Edge("e7", "B", "C", 0.0, {"clear": "clear"}))  # zero-weight cycle
    return graph


def main():
    graph = build_graph()
    rules = RuleSet()

    show(
        "Top-5 legal paths A -> D, initial state 'clear' (no rules)",
        enumerate_paths(graph, rules, "A", "D", "clear", 5),
    )

    v1 = rules.add_rule(
        ForbiddenRule("ban-C-clear", "C", "clear", valid_from=10.0, valid_to=20.0)
    )
    print(f"ruleset version after add_rule: {v1}")
    show(
        "Top-3 at t=15 (rule [10, 20) active: (C, clear) forbidden)",
        enumerate_paths(graph, rules, "A", "D", "clear", 3, at=15.0),
    )
    show(
        "Top-3 at t=20 (interval is left-closed right-open: rule inactive)",
        enumerate_paths(graph, rules, "A", "D", "clear", 3, at=20.0),
    )

    v2 = rules.remove_rule("ban-C-clear")
    print(f"ruleset version after remove_rule: {v2}\n")

    print("== Cursor pagination (page size 2, version", rules.version, ") ==")
    cursor = None
    page = 0
    while True:
        page += 1
        result = enumerate_paths(graph, rules, "A", "D", "clear", 2, cursor=cursor)
        print(f"  page {page}: {[p.edge_ids for p in result.paths]}")
        if result.complete:
            print("  enumeration complete\n")
            break
        cursor = result.cursor

    rules.add_rule(ForbiddenRule("ban-D-loaded", "D", "loaded"))
    print(f"ruleset version after another add_rule: {rules.version}")
    try:
        enumerate_paths(graph, rules, "A", "D", "clear", 2, cursor=cursor)
    except CursorVersionError as exc:
        print(f"  stale cursor rejected: {exc}\n")

    budgeted = enumerate_paths(graph, rules, "A", "D", "clear", 5, max_expansions=2)
    print("== Budget-limited run (max_expansions=2) ==")
    print(
        f"  expansions={budgeted.stats.expansions} "
        f"budget_exhausted={budgeted.stats.budget_exhausted} "
        f"pending_states={budgeted.stats.pending_states}\n"
    )

    report = verify_enumeration(graph, rules, "A", "D", "clear", 10)
    print("== Independent brute-force verification ==")
    print(f"  matches={report.matches} compared={report.compared}")
    print(
        f"  search: expansions={report.search_stats.expansions} "
        f"generated={report.search_stats.generated}"
    )
    print(
        f"  brute force: expansions={report.brute_stats.expansions} "
        f"generated={report.brute_stats.generated}"
    )
    brute_paths, brute_stats = brute_force_paths(
        graph, rules, "A", "D", "clear", max_expansions=2
    )
    print(
        f"  brute force with budget: expansions={brute_stats.expansions} "
        f"budget_exhausted={brute_stats.budget_exhausted} "
        f"pending_states={brute_stats.pending_states}"
    )


if __name__ == "__main__":
    main()
