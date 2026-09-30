"""Runnable samples for timecal (Python 3.11, standard library only).

Run:  python3.11 demo.py
"""

from fractions import Fraction

from timecal import (
    MICROSECOND,
    SECOND,
    UTC,
    CalibratedMap,
    ClockGraph,
    Duration,
    RoundingPolicy,
    SyntheticOffsetTable,
    TimeUnit,
    WallClockTime,
    compare,
    convert_rounded,
    integer_preimages,
    round_fraction,
)


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("1. Exact conversion + rounding policy (round-half-even)")
    third = convert_rounded(Fraction(1, 3), SECOND, MICROSECOND)
    print(f"1/3 s = 1000000/3 us = 333333.333... -> {third} us (nearest)")
    for v in ("1/2", "3/2", "5/2", "-1/2", "-3/2", "-5/2"):
        f = Fraction(v)
        print(f"  half_even({v:>4}) = {round_fraction(f):>2}   "
              f"floor = {round_fraction(f, RoundingPolicy.FLOOR):>2}   "
              f"trunc = {round_fraction(f, RoundingPolicy.TOWARD_ZERO):>2}")

    show("2. Comparison never goes through float")
    big = 2**70
    print(f"compare({big} s, {big * 1_000_000 + 1} us) = "
          f"{compare(big, SECOND, big * 1_000_000 + 1, MICROSECOND)}")
    print(f"  (floats tie here: {float(big) == float(big) + 1e-6})")

    show("3. Huge epoch + tiny interval: no precision loss")
    tick = TimeUnit.sampling(44100)
    t0 = WallClockTime.at(UTC, 9_999_999_999_999_999, SECOND)
    t1 = t0 + Duration.of(1, tick)
    print(f"t1 - t0 = {(t1 - t0).seconds} s  (exactly 1/44100)")
    print(f"  (float epoch absorbs it: {float(9_999_999_999_999_999) + 1 / 44100 == float(9_999_999_999_999_999)})")

    show("4. Calibration graph: cycle identity & contradiction evidence")
    g = ClockGraph()
    g.set_calibration("A", "B", CalibratedMap(Fraction(2), Fraction(1)))
    g.set_calibration("B", "C", CalibratedMap(Fraction(3), Fraction(-1)))
    g.set_calibration("C", "A", CalibratedMap(Fraction(1, 6), Fraction(-1, 3)))
    print("consistent graph cycles:", g.view().check_cycles() or "all identity")
    g.set_calibration("A", "C", CalibratedMap(Fraction(6), Fraction(2)))
    print("multi-path A->C consistent:", g.view().compare_paths("A", "C").consistent)
    g.set_calibration("A", "C", CalibratedMap(Fraction(6), Fraction(5, 2)))
    view = g.view()
    for c in view.check_cycles():
        print("contradiction:", c.describe())
    for d in view.compare_paths("A", "C").discrepancies:
        print("path discrepancy:", " -> ".join(d.path),
              f"differs by offset {d.difference.offset}s")

    show("5. Error bounds: intervals with same-source correlation")
    ab = CalibratedMap(Fraction(2), Fraction(3)).with_error("cal:AB", Fraction(1, 1000))
    bc = CalibratedMap(Fraction(1, 2)).with_error("cal:BC", Fraction(1, 500))
    y = ab.apply(10)
    print(f"A->B(10): center {y.center}, interval [{y.interval().lo}, {y.interval().hi}]")
    x = ab.preimage(y)
    print(f"round trip back: center {x.center}, radius {x.radius} (correlated cancel)")
    composed = ab.then(bc)
    print(f"A->C radius with independent sources: {composed.apply(0).radius}")

    show("6. Inverse mapping may have multiple solutions")
    cal = CalibratedMap(Fraction(2), Fraction(1))
    print(f"ticks x with 5 <= 2x+1 <= 9: {list(integer_preimages(cal, 5, 9))}")

    show("7. Local correction invalidates only related cache")
    g3 = ClockGraph()
    g3.set_calibration("A", "B", CalibratedMap(Fraction(2), Fraction(1)))
    g3.set_calibration("B", "C", CalibratedMap(Fraction(3)))
    g3.set_calibration("X", "Y", CalibratedMap(Fraction(5)))
    v3 = g3.view()
    print(f"v{v3.version} A->C scale: {v3.map('A', 'C').scale} (cached)")
    v3.map("X", "Y")
    g3.set_calibration("B", "C", CalibratedMap(Fraction(4)))  # local correction
    print(f"v{g3.version} A->C scale: {g3.view().map('A', 'C').scale} (recomputed)")
    print(f"old v3 A->C scale still: {g3.at_version(3).map('A', 'C').scale}")
    print(f"unrelated X->Y cache survived: {('X', 'Y') in g3.cache_keys()}")

    show("8. Synthetic offset conversion table (exact)")
    table = SyntheticOffsetTable.synthesize(
        0, 1000, [(1000, Fraction(1, 1000)), (500, Fraction(1, 500))]
    )
    print("tick 1000 ->", table.to_seconds(1000), "s (continuous across rate change)")
    print("tick 1001 ->", table.to_seconds(1001), "s")
    print("1001.0005 s -> tick", table.to_ticks(Fraction(10010005, 10000)),
          "(exact tick 1000.25, half-even)")
    sols = table.ticks_covering(1000, 1001)
    print(f"ticks covering [1000, 1001] s: {sols.start}..{sols.stop - 1} "
          f"({len(list(sols))} solutions)")


if __name__ == "__main__":
    main()
