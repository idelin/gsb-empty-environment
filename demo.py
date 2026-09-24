"""Runnable sample for the pointlocation module (Python 3.11, stdlib only).

Scene: a 12x12 outer square with two explicitly declared holes
(a square hole and a triangular hole).  The outer ring intentionally
repeats its closing vertex to show that normalisation makes it harmless.
"""

from fractions import Fraction as F
from time import perf_counter

from pointlocation import Locator, Location

OUTER = [(0, 0), (12, 0), (12, 12), (0, 12), (0, 0)]  # repeated closing point
HOLE_SQUARE = [(2, 2), (6, 2), (6, 6), (2, 6)]
HOLE_TRIANGLE = [(8, 8), (11, 8), (8, 11)]

PROBES = [
    (F(1, 2), F(1, 2)),   # inside, rational coordinates
    (6, 8),               # inside
    (4, 4),               # inside square hole      -> outside
    (9, 9),               # inside triangular hole  -> outside
    (2, 4),               # on hole edge            -> boundary
    (2, 2),               # hole vertex             -> boundary
    (9, 10),              # on triangle hypotenuse  -> boundary
    (0, 7),               # on outer edge           -> boundary
    (12, 12),             # outer vertex            -> boundary
    (20, 20),             # far away                -> outside
]

MARK = {Location.INSIDE: "#", Location.OUTSIDE: ".", Location.BOUNDARY: "o"}


def main() -> None:
    loc = Locator(OUTER, [HOLE_SQUARE, HOLE_TRIANGLE])

    print("Probes")
    print("------")
    for p in PROBES:
        print(f"  {str(p):>16} -> {loc.locate(p)}")

    print()
    print("Map ('#' inside, '.' outside, 'o' boundary), y from 12 down to 0:")
    rows = []
    for j in range(24, -1, -1):
        y = F(j, 2)
        row = "".join(MARK[loc.locate((F(i, 2), y))] for i in range(25))
        rows.append(f"  {row}")
    print("\n".join(rows))

    batch = [(F(i, 2), F(j, 2)) for i in range(-4, 29) for j in range(-4, 29)]
    start = perf_counter()
    results = loc.locate_many(batch)
    elapsed = perf_counter() - start
    counts = {kind: results.count(kind) for kind in Location}
    print()
    print(f"Batch: {len(batch)} points classified in {elapsed * 1e3:.1f} ms "
          f"via spatial index -> "
          f"inside={counts[Location.INSIDE]}, "
          f"outside={counts[Location.OUTSIDE]}, "
          f"boundary={counts[Location.BOUNDARY]}")


if __name__ == "__main__":
    main()
