"""Runnable demo:  python -m timefit

Fits a synthetic local->reference offset conversion table, rejects outliers
by exact minimum-removal search, and prints corrected reference times with
exact uncertainty ranges.
"""

from __future__ import annotations

from fractions import Fraction

from .piecewise import ExtrapolationError, PiecewiseAffineFitter
from .synth import synthetic_table, true_offset
from .timebase import Duration, Instant, WallClock

_BASE = WallClock.from_iso("2026-01-01T00:00:00+00:00")


def _at(instant):
    """Display an Instant as a wall-clock reading on the demo timeline."""
    return WallClock.from_instant(Instant(_BASE.to_instant().seconds + instant.seconds))


def _show(instant):
    """Wall-clock string, or the exact Fraction if not microsecond-exact."""
    try:
        return _at(instant).isoformat()
    except ValueError:
        return f"{instant.seconds} s (exact, not microsecond-representable)"


def _ms(frac_seconds):
    return f"{float(frac_seconds * 1000):.3f} ms"


def main():
    anchors = synthetic_table()
    drift = Fraction(5, 1000)  # max |slope - 1| = 5 ms/s
    fitter = PiecewiseAffineFitter(anchors, drift, Duration(30))

    print("== synthetic offset conversion table ==")
    print(f"  true offset: 5 s base, +2 ms/s drift up to t=300 s, then -1 ms/s")
    print(f"  noise: uniform within +/-50 ms (anchor error interval)")
    print(f"  injected outliers: #3 shifted +2 s, #8 shifted -1.5 s")
    print(f"  {'idx':>3} {'local (wall clock)':>27} {'measured ref':>27} {'err':>9}")
    for i, a in enumerate(anchors):
        print(f"  {i:>3} {_at(a.local).isoformat():>27} "
              f"{_at(a.ref).isoformat():>27} {_ms(a.error.seconds):>9}")

    print("\n== exact minimum-removal fit (max drift 5 ms/s) ==")
    result = fitter.solve()
    print(f"  status:        {result.status.value}")
    print(f"  min removals:  {result.min_removals}")
    print(f"  removed:       {list(result.removed)}")
    print(f"  conflict cores: {[list(c) for c in result.cores]}")
    print("  witness (local s -> ref s, exact):")
    for local, ref in result.witness:
        print(f"    {local.seconds!s:>4} -> {ref.seconds}")

    print("\n== corrected queries (bounds over ALL feasible mappings) ==")
    for query_s in (0, 90, 300, 333, 660):
        local = Instant(Fraction(query_s))
        qr = fitter.query(local, removed=result.removed)
        print(f"  local {_at(local).isoformat()} [{qr.kind}]")
        print(f"    ref in [{_at(qr.ref_lo).isoformat()}, {_at(qr.ref_hi).isoformat()}]")
        print(f"    corrected {_at(qr.corrected).isoformat()}  "
              f"+/- {_ms(qr.uncertainty.seconds / 2)}")

    print("\n== extrapolation policy (allowed: 30 s past the last knot) ==")
    ok = fitter.query(Instant(Fraction(660 + 20)), removed=result.removed)
    print(f"  last knot +20 s: accepted [{ok.kind}], "
          f"ref in [{_show(ok.ref_lo)}, {_show(ok.ref_hi)}]")
    try:
        fitter.query(Instant(Fraction(660 + 45)), removed=result.removed)
    except ExtrapolationError as exc:
        print(f"  last knot +45 s: rejected ({exc})")

    print("\n== removal budget too small (budget = 1) ==")
    budgeted = fitter.solve(budget=1)
    print(f"  status: {budgeted.status.value}  "
          f"(cannot fit within budget; exact optimum reported separately)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
