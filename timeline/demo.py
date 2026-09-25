"""Runnable example: python3.11 -m timeline.demo"""

from __future__ import annotations

from fractions import Fraction

from .core import Calibration, Timeline
from .fixtures import calibration_shift_alpha, make_blocks


def _show_slice(s) -> str:
    return (
        f"  {s.block_id:>5} v{s.version} @{s.sample_rate}Hz "
        f"samples[{s.sample_start}:{s.sample_end}] "
        f"t=[{float(s.t_start):.4f}, {float(s.t_end):.4f}) "
        f"lead={s.lead} overshoot={s.overshoot}"
    )


def main() -> None:
    tl = Timeline()
    for block in make_blocks():
        tl.add_block(block)
    idx = tl.index()

    print("== resolved slices (non-overlapping, version priority) ==")
    for s in idx.slices:
        print(_show_slice(s))

    print("\n== holes ==")
    for h in idx.holes:
        print(f"  [{float(h.t_start):.4f}, {float(h.t_end):.4f}) reason={h.reason}")

    print("\n== bidirectional queries ==")
    tr = idx.time_range_of("delta", 1, 3)
    print(f"  delta samples[1:3] -> t=[{tr[0]}, {tr[1]})  (rate 8 Hz, not the old 2 Hz)")
    sr = idx.sample_range_of("delta", tr[0], tr[1])
    print(f"  inverse: t=[{tr[0]}, {tr[1]}) -> samples[{sr[0]}:{sr[1]}]")

    print("\n== read [1, 5) by time: valid samples + holes ==")
    result = idx.read(Fraction(1), Fraction(5))
    for seg in result.segments:
        print(
            f"  {seg.block_id:>5} samples[{seg.sample_start}:{seg.sample_end}] "
            f"values={seg.samples} t=[{float(seg.t_start):.4f}, {float(seg.t_end):.4f})"
        )
    for h in result.holes:
        print(f"  HOLE [{float(h.t_start):.4f}, {float(h.t_end):.4f}) reason={h.reason}")

    print("\n== calibration update: historical block shifts, old cursor stable ==")
    old_cursor = tl.cursor()
    before = idx.time_range_of("alpha", 0, 1)
    tl.apply_calibration(Calibration({"alpha": calibration_shift_alpha()}))
    after = tl.index().time_range_of("alpha", 0, 1)
    print(f"  alpha sample 0: before={before[0]}  after={after[0]}  (revision {tl.revision})")
    old_first = next(iter(old_cursor))
    print(f"  old cursor still sees alpha slice at t_start={old_first.t_start} (stable)")

    print("\n== retraction: withdrawing gamma restores shadowed alpha samples ==")
    tl.retract_block("gamma")
    idx2 = tl.index()
    for s in idx2.slices:
        print(_show_slice(s))


if __name__ == "__main__":
    main()
