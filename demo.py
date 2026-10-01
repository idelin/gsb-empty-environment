"""End-to-end demo of the timecore library (Python 3.11, stdlib only)."""

import datetime
from fractions import Fraction

from timecore import (
    AffineCalibration,
    CalibrationGraph,
    ConversionTable,
    Duration,
    ErrorBudget,
    MICROSECOND,
    NANOSECOND,
    SAMPLE_48K,
    SECOND,
    RoundingPolicy,
    TimePoint,
    WallClockTime,
    round_rational,
)


def section(title):
    print(f"\n=== {title} ===")


section("1. Unified units: ms / us / sample ticks, exact rationals")
tick = Duration.of(1, SAMPLE_48K)
print(f"1 tick @48kHz        = {tick.to(MICROSECOND)} us (exact)")
print(f"1 tick @48kHz        = {tick.to_int(MICROSECOND)} us (half-even)")
third = Duration.of(Fraction(1, 3), SECOND)
print(f"1/3 s                = {third.to(MICROSECOND)} us (exact)")
print(f"1/3 s -> us          = {third.to_int(MICROSECOND, RoundingPolicy.HALF_EVEN)} (half-even)")

section("2. Rounding policies, negatives included")
for value in (Fraction(1, 2), Fraction(3, 2), Fraction(-1, 2), Fraction(-3, 2), Fraction(-5, 2)):
    print(
        f"round({value}): half-even={round_rational(value, RoundingPolicy.HALF_EVEN)}, "
        f"half-away={round_rational(value, RoundingPolicy.HALF_AWAY)}, "
        f"floor={round_rational(value, RoundingPolicy.FLOOR)}, "
        f"trunc={round_rational(value, RoundingPolicy.TRUNC)}"
    )

section("3. Wall clock vs time point vs duration; huge epochs stay exact")
w = WallClockTime.from_datetime(
    datetime.datetime(2026, 10, 1, 12, 0, 0, tzinfo=datetime.timezone.utc),
    nanosecond=7,
)
print(f"wall clock           = {w.iso()}")
later = w + Duration.of(1, SAMPLE_48K)
print(f"+1 tick @48kHz       = exact delta {(later - w).seconds} s")
big = TimePoint.of("hw", 2**60, NANOSECOND)
big2 = big + Duration.of(1, NANOSECOND)
print(f"2^60 ns vs +1 ns     : exact compare says less = {big < big2}; "
      f"as floats equal = {float(big.to(NANOSECOND)) == float(big2.to(NANOSECOND))}")

section("4. Calibration graph: multi-path vs direct, cycle audit")
g = CalibrationGraph()
g.add_calibration(AffineCalibration("audio", "mono", Fraction(1), Fraction(5)))
g.add_calibration(AffineCalibration("mono", "net", Fraction(2), Fraction(-3)))
g.add_calibration(AffineCalibration("net", "audio", Fraction(1, 2), Fraction(0)))
for keys, cal in g.compare_paths("audio", "net"):
    print(f"path {keys}: slope={cal.slope}, offset={cal.offset}")
for report in g.audit():
    print(report.describe())

section("5. Intervals with correlated error sources")
g2 = CalibrationGraph()
g2.add_calibration(AffineCalibration("A", "B", Fraction(2), Fraction(0), Fraction(1, 10)))
g2.add_calibration(AffineCalibration("B", "C", Fraction(3), Fraction(0), Fraction(1, 10)))
out = g2.convert_budget(ErrorBudget.exact(1), "A", "C")
print(f"A->C interval        = [{out.lo}, {out.hi}] (center {out.center}, +/-{out.half_width})")
back = g2.convert_budget(out, "C", "A")
print(f"C->A round trip      = [{back.lo}, {back.hi}] (correlated errors cancel)")
cal = AffineCalibration("A", "B", Fraction(2), Fraction(0), error=Fraction(1, 5))
lo, hi = cal.preimage(Fraction(10), Fraction(10))
print(f"preimage of y=10     = [{lo}, {hi}] -- a whole interval of solutions")

section("6. Local correction invalidates only dependent cache entries")
g3 = CalibrationGraph()
g3.add_calibration(AffineCalibration("A", "B", Fraction(1), Fraction(1)))
g3.add_calibration(AffineCalibration("B", "C", Fraction(1), Fraction(1)))
g3.add_calibration(AffineCalibration("C", "D", Fraction(1), Fraction(1)))
print(f"A->C before fix      = {g3.convert(0, 'A', 'C')}")
print(f"C->D before fix      = {g3.convert(0, 'C', 'D')}")
g3.add_calibration(AffineCalibration("A", "B", Fraction(1), Fraction(10)))
history = g3.cache_history("A", "C")
print(f"cache A->C stale     = {[(e.calibration.offset, e.valid) for e in history]}")
print(f"cache C->D untouched = {[(e.calibration.offset, e.valid) for e in g3.cache_history('C', 'D')]}")
print(f"A->C after fix       = {g3.convert(0, 'A', 'C')} (old version kept in history)")
print(f"cache A->C history   = {[(e.calibration.offset, e.valid) for e in g3.cache_history('A', 'C')]}")

section("7. Synthetic offset conversion table (48 kHz ticks -> us)")
table = ConversionTable.build(SAMPLE_48K, MICROSECOND, stride=480, count=11)
print(f"stride=480 ticks, synthetic offset per row = {table.offset_per_stride} us")
print(f"table self-check exact = {table.verify()}")
for tick_count in (0, 1, 123, 480, 481, 4800):
    print(f"tick {tick_count:>5} -> {table.convert(tick_count)} us "
          f"(exact {table.convert_exact(tick_count)})")
