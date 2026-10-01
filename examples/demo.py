"""Runnable tour of the timeproj package.

Run from the repository root:

    python3 examples/demo.py
"""
import sys
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from timeproj.blocks import SamplingBlock
from timeproj.calibration import (
    CalibratedProjector,
    CalibrationStore,
    OffsetTable,
)
from timeproj.compose import compose
from timeproj.projection import project
from timeproj.types import Duration, Interval, TimePoint, WallClockTime


def tp(v):
    return TimePoint(Fraction(v))


def show(result):
    q = result.query
    left = "(" if q.start_open else "["
    right = ")" if q.end_open else "]"
    print(f"  query: {left}{q.start.value}, {q.end.value}{right}"
          f"   -> {result.sample_count} real samples")
    for s in result.slices:
        b = result.blocks[s.key]
        stamps = [b.timestamp(k).value for k in s.indices()]
        print(f"  slice epoch={s.epoch} v={s.version}"
              f" idx[{s.start_index},{s.end_index})  t={stamps}")
    for g in result.gaps:
        print(f"  GAP   [{g.interval.start.value}, {g.interval.end.value})"
              f"  ({g.reason}; reported, never zero-filled)")
    for m in result.missing:
        print(f"  drop  epoch={m.epoch} v={m.version}"
              f" idx={m.index} t={m.timestamp.value} (missing at source)")
    for d in result.dedupe:
        print(f"  dupe  t={d.timestamp.value}: kept {d.kept},"
              f" dropped {d.dropped}")
    for e in result.evidence:
        hit = "exact" if e.exact_hit else "rounded"
        print(f"  evid  epoch={e.epoch} {e.endpoint}: requested"
              f" {e.requested.value} -> {e.direction}({e.raw_value})"
              f" = idx {e.index} ({hit})")


print("=== 0. Three kinds of time, kept apart by the type system ===")
w = WallClockTime(Fraction(1000), clock="gps")
t = TimePoint(Fraction(1000))
print(f"  wall-clock reading: {w.value} on {w.clock!r};"
      f" absolute time point: {t.value}; duration: {Duration(5).value}")
try:
    w - t
except TypeError as exc:
    print(f"  wall - timepoint -> TypeError: {exc}")

print("\n=== 1. Open/closed endpoints and negative times are exact ===")
grid = SamplingBlock(0, 0, tp(0), Fraction(1), tp(-10), tp(10))
show(project(Interval(tp(0), tp(1)), [grid]))                      # [0,1]
show(project(Interval(tp(0), tp(1), end_open=True), [grid]))       # [0,1)
neg = SamplingBlock(0, 0, tp(0), Fraction(2), tp(-10), tp(10))
show(project(Interval(tp(Fraction(-13, 10)), tp(Fraction(-1, 5))), [neg]))

print("\n=== 2. Epochs, rational rates, overlap, missing samples, gaps ===")
a = SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(10),
                  frozenset({3}))            # 1 Hz, sample 3 dropped
b = SamplingBlock(1, 0, tp(12), Fraction(3, 2), tp(12), tp(21))  # 1.5 Hz
c = SamplingBlock(2, 1, tp(5), Fraction(1), tp(5), tp(8))  # overlaps A
result = project(Interval(tp(0), tp(21), end_open=True), [a, b, c])
show(result)

print("\n=== 3. Reverse mapping: actual covered intervals + evidence ===")
for cov in result.reverse():
    iv, cell = cov.interval, cov.cell
    print(f"  epoch={cov.epoch} idx[{cov.start_index},{cov.end_index})"
          f" covers [{iv.start.value}, {iv.end.value}]"
          f" cell [{cell.start.value}, {cell.end.value})"
          f" evidence={len(cov.evidence)}")

print("\n=== 4. Calibration: local correction, partial recompute, cursor ===")
blocks = [
    SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(10)),
    SamplingBlock(1, 0, tp(12), Fraction(1), tp(12), tp(100)),
]
store = CalibrationStore(OffsetTable([(0, 0), (1000, 0)], version=0))
proj = CalibratedProjector(store, blocks)
raw_query = Interval(tp(0), tp(20))
r0 = proj.project_raw(WallClockTime(0), WallClockTime(20))
cursor = proj.open_cursor()
print(f"  v0 corrected query end: {r0.query.end.value},"
      f" samples: {r0.sample_count}")
entries_v0 = dict(proj._states[raw_query].entries)

store.update(OffsetTable([(0, 0), (10, 0), (11, 1), (1000, 1)], version=1),
             raw_span=(10, 11))
r1 = proj.project(raw_query)
print(f"  v1 corrected query end: {r1.query.end.value},"
      f" samples: {r1.sample_count}")
for key, entry in proj._states[raw_query].entries.items():
    fate = "reused (cached)" if entry is entries_v0[key] else "RECOMPUTED"
    print(f"  block epoch={key[0]}: {fate}")
rc = cursor.project(raw_query)
print(f"  cursor (still v{cursor.version}) sees query end"
      f" {rc.query.end.value} with {rc.sample_count} samples")
assert rc.slices == r0.slices and r1.slices != r0.slices

print("\n=== 5. Multi-layer composition equals direct mapping ===")
la = [SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(100))]
lb = [SamplingBlock(0, 0, tp(0), Fraction(1, 2), tp(0), tp(100))]
q = Interval(tp(6), tp(30))
via_a = compose(q, la, lb)
direct = project(q, lb)
print(f"  composed: {[(s.start_index, s.end_index) for s in via_a.slices]}")
print(f"  direct:   {[(s.start_index, s.end_index) for s in direct.slices]}")
assert via_a.slices == direct.slices

print("\nAll demo assertions passed.")
