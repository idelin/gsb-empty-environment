# timecore — exact rational time & multi-clock calibration

Python 3.11, standard library only (`fractions.Fraction` everywhere).
**No float conversion ever happens inside the library**: comparisons,
arithmetic and rounding are exact, so huge epoch timestamps and tiny
sample-clock intervals keep full precision.

## Layout

| File | Contents |
| --- | --- |
| `timecore/rounding.py` | `RoundingPolicy` (half-even, half-away, half-up, floor, ceil, trunc), exact for negatives |
| `timecore/units.py` | `Unit` as rational seconds-per-tick; ms/us/ns unified with sample-clock ticks |
| `timecore/quantities.py` | distinct types `Duration`, `TimePoint`, `WallClockTime` |
| `timecore/errorbudget.py` | `ErrorBudget`: interval with symbolic per-source error terms |
| `timecore/calibration.py` | `AffineCalibration` y = slope·x + offset, exact inverse, interval preimage |
| `timecore/graph.py` | `CalibrationGraph`: path composition, cycle audit, versioned cache |
| `timecore/table.py` | `ConversionTable`: synthetic offset table, exact tick→unit conversion |
| `tests/test_timecore.py` | 19 unittest cases |
| `demo.py` | runnable end-to-end sample |

## Key semantics

- **Types are distinct**: `TimePoint + Duration → TimePoint`,
  `TimePoint − TimePoint → Duration`, `TimePoint + TimePoint` raises
  `TypeError`; comparing points of different clocks raises `ValueError`.
  `WallClockTime` is a separate civil-time type (exact Unix-epoch Fraction).
- **Rounding**: `1/3 s → 333333 us` (half-even); ties go to even
  (`5/2 → 2`), negatives are handled symmetrically (`-3/2 → -2`).
- **Calibration graph**: calibrations are edges; conversions compose along
  the shortest path. `audit()` composes around every cycle and checks for
  the identity; contradictions are reported with residual slope/offset as
  numeric evidence. `compare_paths()` contrasts direct edges with detours.
- **Error bounds**: mapping an `ErrorBudget` yields an interval. Error
  terms are keyed by source calibration, so composing calibrations that
  share a source keeps errors correlated (a forward+inverse round trip
  cancels exactly), while independent sources accumulate worst-case.
  `preimage()` of a monotone calibration with error returns an interval —
  the solution is not unique.
- **Cache versioning**: correcting one calibration bumps its edge version
  and invalidates only cache entries whose path used it; superseded
  entries stay in `cache_history()` (old versions remain inspectable).
- **Conversion table**: precomputed exact integer rows at a fixed stride
  plus exact rational remainders; `verify()` checks every row against
  direct exact computation.

## Run

```sh
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```

## Sample output (`python3.11 demo.py`)

```
=== 1. Unified units: ms / us / sample ticks, exact rationals ===
1 tick @48kHz        = 125/6 us (exact)
1 tick @48kHz        = 21 us (half-even)
1/3 s                = 1000000/3 us (exact)
1/3 s -> us          = 333333 (half-even)

=== 2. Rounding policies, negatives included ===
round(1/2): half-even=0, half-away=1, floor=0, trunc=0
round(3/2): half-even=2, half-away=2, floor=1, trunc=1
round(-1/2): half-even=0, half-away=-1, floor=-1, trunc=0
round(-3/2): half-even=-2, half-away=-2, floor=-2, trunc=-1
round(-5/2): half-even=-2, half-away=-3, floor=-3, trunc=-2

=== 3. Wall clock vs time point vs duration; huge epochs stay exact ===
wall clock           = 2026-10-01T12:00:00.000000007Z
+1 tick @48kHz       = exact delta 1/48000 s
2^60 ns vs +1 ns     : exact compare says less = True; as floats equal = True

=== 4. Calibration graph: multi-path vs direct, cycle audit ===
path (('net', 'audio'),): slope=2, offset=0
path (('audio', 'mono'), ('mono', 'net')): slope=2, offset=7
cycle audio -> net -> mono -> audio: CONTRADICTION, residual slope +0, residual offset -7/2 s

=== 5. Intervals with correlated error sources ===
A->C interval        = [28/5, 32/5] (center 6, +/-2/5)
C->A round trip      = [1, 1] (correlated errors cancel)
preimage of y=10     = [49/10, 51/10] -- a whole interval of solutions

=== 6. Local correction invalidates only dependent cache entries ===
A->C before fix      = 2
C->D before fix      = 1
cache A->C stale     = [(Fraction(2, 1), False)]
cache C->D untouched = [(Fraction(1, 1), True)]
A->C after fix       = 11 (old version kept in history)
cache A->C history   = [(Fraction(2, 1), False), (Fraction(11, 1), True)]

=== 7. Synthetic offset conversion table (48 kHz ticks -> us) ===
stride=480 ticks, synthetic offset per row = 10000 us
table self-check exact = True
tick     0 -> 0 us (exact 0)
tick     1 -> 21 us (exact 125/6)
tick   123 -> 2562 us (exact 5125/2)
tick   480 -> 10000 us (exact 10000)
tick   481 -> 10021 us (exact 60125/6)
tick  4800 -> 100000 us (exact 100000)
```

## Test results (`python3.11 -m unittest discover -s tests`)

```
...................
----------------------------------------------------------------------
Ran 19 tests in 0.007s

OK
```
