"""Runnable sample for the multi-resolution min/max/energy index.

Run:  python3.11 demo.py
"""

from __future__ import annotations

import math
from fractions import Fraction

from multires_index import MultiResolutionIndex
from multires_index.fixtures import waveform


def line(title: str) -> None:
    print(f"\n=== {title} ===")


def main() -> None:
    data = waveform(257)
    spike = data.index(9.0)

    line("build from empty, append in chunks (persistent versions)")
    idx = MultiResolutionIndex(Fraction(44100))
    for i in range(0, len(data), 64):
        idx.append(data[i:i + 64], note=f"append@{i}")
    print(f"length={idx.length}  rate={idx.rate} Hz  versions={idx.head + 1}")

    line("query an unaligned interval: edges exact, energy over true coverage")
    lo, hi = spike - 5, spike + 6
    s = idx.query(lo, hi)
    print(f"[{lo}, {hi}) samples={s.samples} present={s.present} "
          f"missing={s.missing} complete={s.complete}")
    print(f"min={s.minimum:.4f} max={s.maximum:.4f} "
          f"energy={s.energy:.6f} mean_energy={s.mean_energy:.6f}")
    print(f"buckets={len(s.buckets)} full={s.full_buckets} "
          f"partial={s.partial_buckets} visits={s.visits}")
    print("boundary bucket levels:", s.boundary_levels)

    line("peak provenance: all tied aligned sources and exact samples")
    pk = idx.peak(0, len(data))
    print(f"peak={pk.peak} aligned_source_ranges={pk.ranges}")
    print(f"exact_sample_runs={pk.sample_ranges} indices={pk.sample_indices}")

    line("range outside the spike cannot contain it")
    side = idx.peak(spike + 1, len(data))
    print(f"peak on right side = {side.peak}")

    line("incremental interval replacement with locality counters")
    vid = idx.replace_range(10, 14, [4.0, -4.0, 4.0, -4.0], note="splice")
    v = idx.version(vid)
    print(f"version {v.version_id}: copied={v.copied} reused={v.reused} "
          f"visits={v.visits} parent={v.parent_id}")

    line("withdraw samples -> missing markers, no shift; then restore")
    idx.withdraw(20, 23)
    w = idx.query(18, 25)
    print(f"present={w.present} missing={w.missing} max={w.maximum}")
    idx.restore(20, 23, data[20:23])
    print("after restore present =", idx.query(18, 25).present)

    line("exact rational rate change")
    idx.change_rate(Fraction(48000))
    print(f"rate={idx.rate}  time of sample {spike} = "
          f"{float(idx.time_at_index(spike)):.6f} s exactly "
          f"{idx.time_at_index(spike)}")

    line("empty interval has no phantom peak")
    empty = idx.peak(30, 30)
    print(f"peak={empty.peak} ranges={empty.ranges}")

    line("versions are persistent; rollback to build head")
    rb = idx.rollback(1)
    print(f"rolled back to version 1 via new version {rb}; "
          f"length={idx.length} rate={idx.rate}")


if __name__ == "__main__":
    main()
