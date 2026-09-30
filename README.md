# timecal — exact rational time conversion & clock calibration

Python 3.11, standard library only (`fractions.Fraction` everywhere).
No floating point is ever used for conversion, comparison or calibration
arithmetic, so huge epoch timestamps and tiny tick intervals keep full
precision.

## Layout

- `timecal/rounding.py` — rounding policies (`HALF_EVEN`, `HALF_AWAY`,
  `FLOOR`, `CEILING`, `TOWARD_ZERO`) on exact rationals, correct for negatives
- `timecal/units.py` — unified s / ms / us / ns / sampling-tick units,
  exact conversion, float-free `compare`
- `timecal/core.py` — distinct types `WallClockTime` / `TimePoint` /
  `Duration` with checked algebra (point+duration, point−point, domain checks)
- `timecal/calibration.py` — rational affine calibrations organized as a
  versioned graph: cycle-identity checks with contradiction evidence,
  multi-path vs direct comparison, interval error bounds with same-source
  correlation, selective cache invalidation across versions
- `timecal/table.py` — synthetic offset conversion tables (piecewise-affine,
  continuity-preserving, exact both directions)
- `tests/` — 31 unittest cases; `demo.py` — runnable samples

## Key semantics

- **Rounding**: `1/3 s -> 333333 us` under round-half-even; ties go to even
  (`3/2 -> 2`, `-3/2 -> -2`).
- **Errors**: a calibration carries error terms keyed by source. Composing a
  map with its own inverse cancels the correlated error exactly (radius 0);
  independent sources accumulate worst-case.
- **Inverse maps are sets**: `integer_preimages` / `ticks_covering` return
  all solutions, not one.
- **Versioning**: `set_calibration` creates a new graph version; only cache
  entries depending on the corrected edge are dropped, and
  `graph.at_version(v)` still serves old versions unchanged.

## Sample run (`python3.11 demo.py`)

```
=== 1. Exact conversion + rounding policy (round-half-even) ===
1/3 s = 1000000/3 us = 333333.333... -> 333333 us (nearest)
  half_even( 1/2) =  0   floor =  0   trunc =  0
  half_even( 3/2) =  2   floor =  1   trunc =  1
  half_even( 5/2) =  2   floor =  2   trunc =  2
  half_even(-1/2) =  0   floor = -1   trunc =  0
  half_even(-3/2) = -2   floor = -2   trunc = -1
  half_even(-5/2) = -2   floor = -3   trunc = -2

=== 2. Comparison never goes through float ===
compare(1180591620717411303424 s, 1180591620717411303424000001 us) = -1
  (floats tie here: True)

=== 3. Huge epoch + tiny interval: no precision loss ===
t1 - t0 = 1/44100 s  (exactly 1/44100)
  (float epoch absorbs it: True)

=== 4. Calibration graph: cycle identity & contradiction evidence ===
consistent graph cycles: all identity
multi-path A->C consistent: True
contradiction: cycle A -> B -> C -> A; offset residual -1/12s
contradiction: cycle A -> C -> B -> A; offset residual 1/12s
contradiction: cycle B -> C -> A -> B; offset residual -1/6s
path discrepancy: A -> B -> C differs by offset 1/2s

=== 5. Error bounds: intervals with same-source correlation ===
A->B(10): center 23, interval [22999/1000, 23001/1000]
round trip back: center 10, radius 0 (correlated cancel)
A->C radius with independent sources: 1/400

=== 6. Inverse mapping may have multiple solutions ===
ticks x with 5 <= 2x+1 <= 9: [2, 3, 4]

=== 7. Local correction invalidates only related cache ===
v3 A->C scale: 6 (cached)
v4 A->C scale: 8 (recomputed)
old v3 A->C scale still: 6
unrelated X->Y cache survived: True

=== 8. Synthetic offset conversion table (exact) ===
tick 1000 -> 1001 s (continuous across rate change)
tick 1001 -> 500501/500 s
1001.0005 s -> tick 1000 (exact tick 1000.25, half-even)
ticks covering [1000, 1001] s: 0..1000 (1001 solutions)
```

## Test results (`python3.11 -m unittest discover -s tests -t .`)

```
Ran 31 tests in 0.012s

OK
```
