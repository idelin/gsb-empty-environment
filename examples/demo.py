"""End-to-end demo of the timemap library."""
import sys
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from timemap import (Duration, Evidence, Interval, LogicalTime, MapHistory,
                     OffsetTable, PiecewiseMap, WallClock, check_composition)

print("=" * 72)
print("1. Piecewise-affine map: play / pause / 2x speed / reverse")
print("=" * 72)
f = PiecewiseMap.build(
    0, 0, [(5, 1), (5, 0), (10, 2), (10, -1)],
    evidence=(Evidence("ingest", "original timeline"),),
)
print(f)

print("\n-- pause [5,10) maps both bounds to one logical point")
print("f(5) =", f.eval(5), " f(10) =", f.eval(10))

print("\n-- image of the whole real domain [0, 30]")
print("image =", f.image(f.domain()))

print("\n-- pre-image of logical point 5 (boundary choices kept)")
print("preimage({5}) =", f.preimage(Interval.point(5)))

print("\n-- pre-image of logical interval [20, 25] (forward + reverse parts)")
print("preimage([20,25]) =", f.preimage(Interval(20, 25, hi_open=False)))

print()
print("=" * 72)
print("2. Composition with a second map, checked by rational enumeration")
print("=" * 72)
g = PiecewiseMap.build(
    0, 0, [(10, 1), (5, 0), (15, Fraction(1, 2))],
    evidence=(Evidence("mixer", "output timeline"),),
)
h = f.compose(g)
print(h)
result = check_composition(f, g, h)
print("\ncomposition check h == g . f:", result)
print("h(13) =", h.eval(13), " preimage({10}) =", h.preimage(Interval.point(10)))

print()
print("=" * 72)
print("3. Versioned local revision: insert an ad-break pause at real [12, 14)")
print("=" * 72)
hist = MapHistory(f)
v2 = hist.revise(1, 12, 14, [(2, 0)], reason="ad break",
                 evidence=(Evidence("editor", "insert ad break pause"),))
m2 = hist.version(v2)
print(m2)
print("\nnew pause: v2 f(12) =", m2.eval(12), " f(14) =", m2.eval(14))
print("old version still answers: v1 f(13) =", hist.version(1).eval(13),
      " v2 f(13) =", m2.eval(13))
print("revision log:", *hist.revisions, sep="\n  ")
print("evidence at v2 t=13:", [str(e) for e in hist.evidence_at(v2, 13)])
print("evidence at v1 t=13:", [str(e) for e in hist.evidence_at(1, 13)])

print()
print("=" * 72)
print("4. Offset conversion table with exact wall-clock / logical types")
print("=" * 72)
table = OffsetTable.from_map(f)
for e in table.entries:
    close = ")" if e.end_open else "]"
    print(f"  real [{e.real_start}, {e.real_end}{close} "
          f"logical = {e.logical_start} + (real - {e.real_start}) * {e.rate}")
w = WallClock(Fraction(25, 2))
lt = table.to_logical(w)
print(f"\n{w} -> {lt}")
print("wall-clock choices for LogicalTime(5):", table.to_real(LogicalTime(5)))
print("typed arithmetic:", WallClock(10), "-", WallClock(4), "=", WallClock(10) - WallClock(4))
print("typed arithmetic:", WallClock(4), "+", Duration(6), "=", WallClock(4) + Duration(6))

print()
print("=" * 72)
print("5. Huge intervals stay breakpoint-based (no tick expansion)")
print("=" * 72)
big = 10**15
huge = PiecewiseMap.build(0, 0, [(big, 1), (big, 0), (big, 3)])
print(f"domain [0, {3 * big}] with {len(huge.segments)} segments")
print("image =", huge.image(huge.domain()))
print(f"preimage({{{big}}}) =", huge.preimage(Interval.point(big)))
