"""End-to-end demo: recover wall-clock time from sample sequence numbers.

Scenario: a 1000 Hz sampler with up to 100 ppm drift. Calibration anchors
arrive over time (some late), one epoch reset occurs, samples are dropped,
and one calibration point contradicts the others.
"""
from __future__ import annotations

import sys
from fractions import Fraction
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from clockmap import (  # noqa: E402
    Anchor,
    ClockMapper,
    Duration,
    EpochBoundaryError,
    OffsetTable,
    SlopeModel,
    WallTime,
)

BASE = Fraction(1_700_000_000)  # wall-clock reference, seconds


def show(title):
    print(f"\n=== {title} ===")


def main():
    slope = SlopeModel.from_ppm(sample_rate_hz=1000, ppm=100)
    print(f"slope bounds: {slope.min_period} .. {slope.max_period} s/sample "
          f"(nominal {slope.nominal_period})")
    mapper = ClockMapper(slope)

    show("1. Calibration anchors with error intervals (epoch 0)")
    a = Anchor(0, 1000, BASE + 1 - Fraction(5, 1000), BASE + 1 + Fraction(5, 1000), "cal-A")
    b = Anchor(0, 5000, BASE + 5 - Fraction(5, 1000), BASE + 5 + Fraction(5, 1000), "cal-B")
    mapper.add_anchor(a)
    mapper.add_anchor(b)
    print(f"added {a.name()}: seq {a.seq} -> wall [{a.wall_lo}, {a.wall_hi}]")
    print(f"added {b.name()}: seq {b.seq} -> wall [{b.wall_lo}, {b.wall_hi}]")

    show("2. Feasible wall-clock interval for seq 3000 (piecewise, not uniform)")
    est = mapper.map_seq(0, 3000)
    print(f"feasible interval: [{est.interval.lo.seconds}, {est.interval.hi.seconds}]")
    print(f"width: {est.interval.width.seconds} s  (evidence: {est.evidence})")

    show("3. Dropped samples: seq jumps 2000 -> 2010")
    lo, hi = mapper.duration_between(0, 2000, 0, 2010)
    print(f"9 samples dropped, but 10 periods elapse: duration in "
          f"[{lo.seconds}, {hi.seconds}] s")

    show("4. Late calibration locally revises the mapping")
    late = Anchor(0, 2900, BASE + Fraction(29, 10) - Fraction(2, 1000),
                  BASE + Fraction(29, 10) + Fraction(2, 1000), "cal-late")
    mapper.add_anchor(late)
    revised = mapper.map_seq(0, 3000)
    print(f"revised interval: [{revised.interval.lo.seconds}, "
          f"{revised.interval.hi.seconds}] (version {revised.version})")
    print(f"previous estimate superseded: {est.superseded}; "
          f"kept as evidence chain: v{est.version} -> v{revised.version}")

    show("5. Epoch reset cannot be bridged")
    mapper.add_anchor(Anchor(1, 10, BASE + 100, BASE + 100, "cal-reset"))
    try:
        mapper.duration_between(0, 4000, 1, 10)
    except EpochBoundaryError as exc:
        print(f"refused: {exc}")
    iv = mapper.feasible_interval(1, 10)
    print(f"epoch 1 seq 10 maps to [{iv.lo.seconds}, {iv.hi.seconds}] "
          "using only epoch-1 anchors")

    show("6. Contradictory calibration -> anomaly + minimal conflict set")
    bad = Anchor(0, 5000, BASE + 2, BASE + 2, "cal-bad")  # wall rolled back
    conflicts = mapper.add_anchor(bad)
    for c in conflicts:
        print(f"conflict: {c.first.name()} vs {c.second.name()}: {c.reason}")
    minimal = mapper.minimal_conflict_set(0)
    print(f"minimal conflict set: {[x.name() for x in minimal]}")
    print(f"cal-bad flagged anomalous: {mapper.is_anomalous(bad)}")

    show("7. Synthetic offset conversion table (exact, epoch 0 before cal-bad)")
    clean = ClockMapper(slope)
    clean.add_anchor(a)
    clean.add_anchor(b)
    table = OffsetTable.from_mapper(clean, 0)
    iv = table.to_wall(0, 3000)
    print(f"seq 3000 -> wall [{iv.lo.seconds}, {iv.hi.seconds}]")
    lo_seq, hi_seq = table.to_seq(0, iv.lo)
    print(f"wall {iv.lo.seconds} -> seq in [{lo_seq}, {hi_seq}]")

    show("8. Exact arithmetic: huge neighbouring timestamps")
    t1 = WallTime(Fraction(2**60) + Fraction(1, 2**30))
    t2 = t1 + Duration(Fraction(1, 2**30))
    print(f"t2 - t1 = {(t2 - t1).seconds} s exactly; "
          f"as float64 they are equal: {float(t1) == float(t2)}")


if __name__ == "__main__":
    main()
