"""Shared demo graph used by tests and the example script.

Layout (states: "clear", "loaded")::

    A --e1/1--> B (clear -> loaded)        C --e5/0--> D
    A --e2/1--> B (clear -> clear)         C --e6/0--> B
    A --e4/2--> C (clear -> clear)         B --e7/0--> C
    B --e3/1--> D (loaded -> clear, clear -> clear)
    E (isolated vertex)

e6/e7 form a zero-weight cycle B <-> C. Legal product-simple paths A -> D
with initial state "clear", in (cost, edge count, edge IDs) order:

    1. (1, 3, e2 e7 e5)
    2. (2, 2, e1 e3)
    3. (2, 2, e2 e3)
    4. (2, 2, e4 e5)
    5. (3, 3, e4 e6 e3)
"""

from product_paths import Edge, Graph


def build_demo_graph() -> Graph:
    graph = Graph()
    graph.add_vertex("E")  # isolated vertex is kept
    graph.add_edge(Edge("e1", "A", "B", 1.0, {"clear": "loaded"}))
    graph.add_edge(Edge("e2", "A", "B", 1.0, {"clear": "clear"}))
    graph.add_edge(Edge("e3", "B", "D", 1.0, {"loaded": "clear", "clear": "clear"}))
    graph.add_edge(Edge("e4", "A", "C", 2.0, {"clear": "clear"}))
    graph.add_edge(Edge("e5", "C", "D", 0.0, {"clear": "clear"}))
    graph.add_edge(Edge("e6", "C", "B", 0.0, {"clear": "clear"}))
    graph.add_edge(Edge("e7", "B", "C", 0.0, {"clear": "clear"}))
    return graph


EXPECTED_EDGE_IDS = [
    ("e2", "e7", "e5"),
    ("e1", "e3"),
    ("e2", "e3"),
    ("e4", "e5"),
    ("e4", "e6", "e3"),
]
