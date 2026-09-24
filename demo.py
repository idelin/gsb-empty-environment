"""Sample run of polytopo: shared-edge adjacency, point contacts, snapping.

Run with: python3.11 demo.py
"""

from fractions import Fraction

from polytopo import analyze_adjacency, snap_polygon_to_polygon


def show(title, adj):
    print(f"== {title} ==")
    print(f"  edge-adjacent : {adj.is_edge_adjacent}")
    print(f"  shared length : {adj.shared_length}")
    for seg in adj.shared_segments:
        print(f"  shared segment: {seg.start} -> {seg.end}  (len {seg.length})")
    for p in adj.point_contacts:
        print(f"  point contact : {p}")
    if not adj.point_contacts:
        print("  point contact : (none)")
    print()


# 1. Full shared edge.
a = [(0, 0), (2, 0), (2, 2), (0, 2)]
b = [(2, 0), (4, 0), (4, 2), (2, 2)]
show("full shared edge", analyze_adjacency(a, b))

# 2. One long edge of A against two short edges of B (collinear vertex).
b_split = [(2, 0), (4, 0), (4, 2), (2, 2), (2, 1)]
show("long edge vs two short edges", analyze_adjacency(a, b_split))

# 3. Point contact only -> not edge adjacency.
c = [(2, 2), (4, 2), (4, 4), (2, 4)]
show("corner touch (point contact only)", analyze_adjacency(a, c))

# 4. T-junction: vertex of D on the midpoint of A's edge.
d = [(2, 1), (3, 0), (3, 2)]
show("T-junction (point contact only)", analyze_adjacency(a, d))

# 5. Duplicate edges in B are not double counted.
b_dup = [(2, 0), (4, 0), (4, 1), (2, 1), (2, 0), (2, 1)]
show("duplicate edge counted once", analyze_adjacency(a, b_dup))

# 6. Rational coordinates, exact Fraction result.
e = [(0, 0), (Fraction(3, 2), 0), (Fraction(3, 2), 1), (0, 1)]
f = [(Fraction(3, 2), Fraction(1, 3)), (3, Fraction(1, 3)), (3, 1), (Fraction(3, 2), 1)]
show("rational coordinates", analyze_adjacency(e, f))

# 7. Tolerance snapping: displacement reported, unrelated vertices untouched.
print("== tolerance snapping ==")
eps = Fraction(1, 100)
g = [(2 + eps, 0), (4, 0), (4, 2), (2 + eps, 2)]
print(f"  before snap: edge-adjacent = {analyze_adjacency(a, g).is_edge_adjacent}")
snapped, events = snap_polygon_to_polygon(g, a, Fraction(1, 10))
for ev in events:
    print(
        f"  vertex {ev.index}: {ev.original} -> {ev.result}  "
        f"displacement {ev.displacement}  |d|^2 = {ev.distance_squared}"
    )
untouched = [i for i in range(len(g)) if all(ev.index != i for ev in events)]
print(f"  untouched vertices (bit-identical): {untouched}")
adj = analyze_adjacency(a, snapped)
print(
    f"  after snap : edge-adjacent = {adj.is_edge_adjacent}, "
    f"shared length = {adj.shared_length}"
)
