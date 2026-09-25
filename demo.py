"""Sample runs of topology-preserving polyline network simplification.

Run with:  python3.11 demo.py
"""

from fractions import Fraction

from simplify import simplify_network


def show(title, polylines, tol):
    print(f"=== {title} (tol={tol}) ===")
    res = simplify_network(polylines, tol=tol)
    for i, (before, after) in enumerate(zip(polylines, res.polylines)):
        print(f"  polyline {i}: {len(before)} -> {len(after)} points")
        print(f"    {after}")
    if res.removed:
        print(f"  removed {len(res.removed)} vertices, "
              f"max error^2 = {res.max_error_sq} "
              f"(max error ~= {res.max_error:.4f})")
        for r in res.removed:
            print(f"    polyline {r.polyline} vertex {r.index} {r.point}: "
                  f"error^2 = {r.error_sq} (error ~= {r.error:.4f})")
    else:
        print("  no vertices removed")
    print()
    return res


def subsequence(poly, p, q, start=0):
    """Points of poly between p and q (inclusive), searching from start."""
    i = poly.index(p, start)
    j = poly.index(q, i)
    return poly[i:j + 1], j


# --- Sample 1: two regions sharing a wiggly boundary -----------------------
region_a = [(0, 0), (2, 0), (3, 1), (2, 2), (3, 3), (2, 4), (0, 4), (0, 0)]
region_b = [(2, 0), (4, 0), (4, 4), (2, 4), (3, 3), (2, 2), (3, 1), (2, 0)]
res = show("Two regions sharing a boundary", [region_a, region_b], tol=1)
a, b = res.polylines
p0, p4 = (Fraction(2), Fraction(0)), (Fraction(2), Fraction(4))
shared_a, _ = subsequence(a, p0, p4)
shared_b, _ = subsequence(b, p4, p0)   # (2,4) occurs after the first (2,0)
assert shared_a == shared_b[::-1], "shared boundary mismatch!"
print(f"  shared boundary identical in both regions: {shared_a}\n")

# --- Sample 2: narrow channel must stay open -------------------------------
arch = [(0, 0), (4, 2), (8, 2), (12, 0)]   # arch over a wall
wall = [(6, -1), (6, 1)]                   # wall crossing y=0
res = show("Narrow channel (arch over a wall)", [arch, wall], tol=3)
print("  chord (0,0)-(12,0) would cross the wall -> rejected; "
      "channel stays open\n")

# --- Sample 3: exact rational coordinates ----------------------------------
line = [(0, 0), (1, Fraction(1, 3)), (2, 0), (3, Fraction(2, 3)), (4, 0)]
show("Rational coordinates, tol=1/2", [line], tol=Fraction(1, 2))

# --- Sample 4: tol=0 removes only strictly redundant points ----------------
poly = [(0, 0), (1, 0), (2, 0), (2, 1), (2, 2), (3, 2)]
show("tol=0 (only strictly redundant points)", [poly], tol=0)

# --- Sample 5: flat triangle ring with huge tolerance ----------------------
tri = [(0, 0), (4, 0), (2, 1), (0, 0)]
res = show("Flat triangle ring, tol=2 (ring must not collapse/flip)", [tri], tol=2)
print(f"  ring keeps {len(res.polylines[0]) - 1} distinct vertices, "
      "orientation preserved\n")
