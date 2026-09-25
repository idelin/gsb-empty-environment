"""End-to-end demo of clockmap.

Run from the repository root:

    python3.11 examples/demo.py
"""

import os
import sys
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from clockmap import (  # noqa: E402
    Anchor,
    PiecewiseClockMapper,
    SlopeBounds,
    WallClock,
    find_drops,
)

SLOPES = SlopeBounds.from_nominal_rate(10, Fraction(1, 10))  # 10 Hz, +/-10% drift


def anchor(epoch, index, lo, hi, label=""):
    return Anchor(epoch, index, WallClock(Fraction(lo)), WallClock(Fraction(hi)), label)


def show_interval(title, iv):
    print(f"  {title}: [{iv.earliest.seconds}, {iv.latest.seconds}]"
          f"  (width {iv.width.seconds} s, feasible={iv.feasible})")


def main():
    print("=== 1. piecewise mapping from indices + calibration intervals ===")
    mapper = PiecewiseClockMapper(10, SLOPES)
    mapper.add_anchor(anchor(0, 0, 100, 101, "boot"))
    mapper.add_anchor(anchor(0, 100, 110, 111, "ntp"))
    print("  synthetic offset conversion table, epoch 0:")
    for seg in mapper.conversion_table(0):
        lo = ", ".join(f"t >= {l.slope}*n + ({l.offset})" for l in seg.lower.lines)
        hi = ", ".join(f"t <= {l.slope}*n + ({l.offset})" for l in seg.upper.lines)
        print(f"    n in [{seg.first_index}, {seg.last_index}]: {lo} ; {hi}")
    show_interval("map(0, 50) ", mapper.map_index(0, 50))
    show_interval("map(0, 150)", mapper.map_index(0, 150))

    print("\n=== 2. dropped samples: a jump of 10 keeps its time interval ===")
    observed = list(range(0, 50)) + list(range(59, 100))
    print(f"  drops in observed stream: {find_drops(observed)}")
    iv49, iv59 = mapper.map_index(0, 49), mapper.map_index(0, 59)
    print(f"  lower track: t(59)-t(49) = {(iv59.earliest - iv49.earliest).seconds} s")
    print(f"  upper track: t(59)-t(49) = {(iv59.latest - iv49.latest).seconds} s")
    show_interval("dropped index 54 still maps", mapper.map_index(0, 54))

    print("\n=== 3. late calibration revises the mapping, evidence is kept ===")
    revision = mapper.add_anchor(anchor(0, 50, 105, 106, "late-ntp"))
    print(f"  revision v{revision.version}: feasible={revision.feasible},"
          f" revised {len(revision.updates)} earlier result(s)")
    for record, new in revision.updates:
        old = record.interval
        print(f"    map(0, {record.index}): [{old.earliest.seconds}, {old.latest.seconds}]"
              f" -> [{new.earliest.seconds}, {new.latest.seconds}] (old kept as evidence)")
    print(f"  evidence log size: {len(mapper.evidence)}"
          f" (superseded: {sum(r.superseded for r in mapper.evidence)})")

    print("\n=== 4. epoch reset with rollback: epochs are never bridged ===")
    rev = mapper.add_anchor(anchor(1, 0, 50, 51, "reboot"))
    print(f"  new epoch: {rev.new_epoch}, rollback detected: {rev.rollback_detected}")
    show_interval("map(1, 10) uses only epoch-1 anchors", mapper.map_index(1, 10))
    show_interval("map(0, 50) unchanged by epoch 1", mapper.map_index(0, 50))

    print("\n=== 5. inconsistent calibration -> anomaly + minimal conflict set ===")
    rev = mapper.add_anchor(anchor(0, 200, 1000, 1001, "bad"))
    print(f"  status(0) = {mapper.status(0)!r} after anchor 'bad'")
    print(f"  minimal conflict set: {[a.label for a in rev.conflict_set]}")

    print("\n=== 6. huge nearby timestamps stay exact (no float swallowing) ===")
    base = 10**18
    us = Fraction(1, 10**6)
    big = PiecewiseClockMapper(10, SLOPES)
    big.add_anchor(anchor(0, 0, base + us, base + 2 * us))
    big.add_anchor(anchor(0, 100, base + 10 + us, base + 10 + 2 * us))
    iv = big.map_index(0, 50)
    lo, hi = iv.earliest.seconds, iv.latest.seconds
    print(f"  band at n=50: [{lo}, {hi}]")
    print(f"  exact width: {hi - lo} s; as float both ends are {float(lo)!r}")


if __name__ == "__main__":
    main()
