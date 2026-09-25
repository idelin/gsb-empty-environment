"""Runnable examples for polygon_adjacency (Python 3.11, stdlib only)."""

from fractions import Fraction as F

from polygon_adjacency import analyze


def show(title, report):
    print(f"=== {title} ===")
    print(report.summary())
    print()


# 1. Two building footprints sharing a full wall (integer coordinates).
show(
    "1. full shared wall",
    analyze(
        [(0, 0), (4, 0), (4, 3), (0, 3)],
        [(4, 0), (9, 0), (9, 3), (4, 3)],
    ),
)

# 2. One long edge of A matched by two short edges of B (split vertex):
#    the shared length aggregates into a single maximal shared edge.
show(
    "2. long edge vs two short edges",
    analyze(
        [(0, 0), (10, 0), (10, 2), (0, 2)],
        [(0, 0), (4, 0), (10, 0), (10, -2), (0, -2)],
    ),
)

# 3. Pure point contact: corner to corner, no shared edge.
show(
    "3. corner touch only",
    analyze(
        [(0, 0), (1, 0), (1, 1), (0, 1)],
        [(1, 1), (3, 1), (3, 2), (1, 2)],
    ),
)

# 4. Survey data with rational coordinates: B is off by 1/100.
#    Without snapping the near miss is reported exactly (no shared edge);
#    with an explicit tolerance every displacement is reported.
b_shifted = [
    (F(401, 100), F(1, 100)),
    (F(901, 100), F(1, 100)),
    (F(901, 100), F(301, 100)),
    (F(401, 100), F(301, 100)),
]
a_block = [(0, 0), (4, 0), (4, 3), (0, 3)]
show("4a. rational near miss, no tolerance", analyze(a_block, b_shifted))
show(
    "4b. same input, snap tolerance 1/10",
    analyze(a_block, b_shifted, snap_tolerance=F(1, 10)),
)

# 5. Diagonal shared edge: exact squared length, irrational length.
show(
    "5. diagonal shared edge",
    analyze(
        [(0, 0), (2, 0), (2, 2)],
        [(0, 0), (0, 2), (2, 2)],
    ),
)
