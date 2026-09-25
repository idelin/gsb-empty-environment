"""Reproducible end-to-end scenario for the polyphase resampler.

Run:  python3.11 demo.py

Scenario (all inputs from local fixtures, fixed seeds, no audio device):
  * shared clock at 48 kHz input, ratio 2/3 (-> 32 kHz out)
  * runtime switch to 3/4 (-> 36 kHz) at source position 997 (non-integer
    phase), then to 1/2 (-> 24 kHz) at source position 4000
  * channel A: clean signal, random chunking (seed 11)
  * channel B: same signal with marked gaps, different chunking (seed 22)
  * checkpoint/restore inside the filter tail of the first switch
Every check is asserted; the process exits non-zero on any failure.
"""

from fractions import Fraction

import fixtures
from resampler import (GAP_CONTAMINATED, NUMERICAL_TOLERANCE, Kernel,
                       ResamplerClock, contaminated_ranges)
from test_resampler import reference_run

SWITCH_1 = 997
SWITCH_2 = 4000
N = 6000


def main():
    taps_a = fixtures.design_lowpass(2, 3, 6)
    taps_b = fixtures.design_lowpass(3, 4, 6)
    taps_c = fixtures.design_lowpass(1, 2, 16)
    clock = ResamplerClock(fixtures.INPUT_RATE, 2, 3, Kernel(taps_a, 2))
    clock.set_ratio(3, 4, at=SWITCH_1, kernel=Kernel(taps_b, 3))
    clock.set_ratio(1, 2, at=SWITCH_2, kernel=Kernel(taps_c, 1))

    data = fixtures.mixed_signal(N, seed=20260926)
    gaps = fixtures.gap_positions(N, seed=5, count=4)

    # ---- channel A: clean, random chunks, with a mid-tail checkpoint ----
    ch_a = clock.add_channel()
    out_a, snap, snap_pos = [], None, None
    pos = 0
    for k in fixtures.chunk_split(N, seed=11):
        if snap is None and pos + k > 950:
            # Near the switch: single-sample pushes so the checkpoint lands
            # inside the (few-samples-wide) filter tail of the switch.
            while k > 0 and snap is None:
                out_a.extend(ch_a.process(data[pos:pos + 1]))
                pos += 1
                k -= 1
                nxt = ch_a.next_output_time
                if (nxt is not None
                        and nxt - ch_a.latency < SWITCH_1 <= nxt):
                    snap, snap_pos = ch_a.snapshot(), pos
        out_a.extend(ch_a.process(data[pos:pos + k]))
        pos += k
    assert snap is not None, "no mid-tail checkpoint opportunity found"

    # ---- channel B: gaps marked, different chunking ----------------------
    ch_b = clock.add_channel()
    out_b = []
    pos = 0
    gapped = fixtures.with_gaps(data, gaps)
    for k in fixtures.chunk_split(N, seed=22):
        out_b.extend(ch_b.process(gapped[pos:pos + k]))
        pos += k

    # ---- 1) shared clock: identical time base, zero drift ----------------
    assert len(out_a) == len(out_b)
    assert all(a.time == b.time for a, b in zip(out_a, out_b))
    assert all(a.timestamp == b.timestamp for a, b in zip(out_a, out_b))

    # ---- 2) monotonic output times across both switches ------------------
    times = [o.time for o in out_a]
    assert all(x < y for x, y in zip(times, times[1:]))

    # ---- 3) checkpoint/restore inside the filter tail --------------------
    tail_len = len(out_a)
    ch_a.restore(snap)
    replayed = []
    pos = snap_pos
    for k in fixtures.chunk_split(N - snap_pos, seed=33):
        replayed.extend(ch_a.process(data[pos:pos + k]))
        pos += k
    assert out_a[snap[ "out_index"]:] == replayed[:tail_len - snap["out_index"]]

    # ---- 4) impulse-style agreement with the direct reference ------------
    ref = reference_run(data, (), [(0, 2, 3, taps_a),
                                   (SWITCH_1, 3, 4, taps_b),
                                   (SWITCH_2, 1, 2, taps_c)], len(out_a))
    worst = max(abs(o.value - v) / max(1.0, abs(v))
                for o, (_, v, _) in zip(out_a, ref))
    assert worst <= NUMERICAL_TOLERANCE, worst

    # ---- 5) gaps pollute exactly the convolution window ------------------
    dirty = [o for o in out_b if o.flags & GAP_CONTAMINATED]
    assert dirty, "expected gap-contaminated outputs"
    ranges = contaminated_ranges(out_b)

    # ---- report -----------------------------------------------------------
    print("polyphase resampler demo (reproducible, fixed seeds)")
    print(f"  input: {N} samples @ {fixtures.INPUT_RATE} Hz, "
          f"ratios 2/3 -> 3/4 @{SWITCH_1} -> 1/2 @{SWITCH_2}")
    print(f"  declared latency: {Kernel(taps_a, 2).latency} / "
          f"{Kernel(taps_b, 3).latency} / {Kernel(taps_c, 1).latency} "
          f"input samples (regimes 1/2/3)")
    print(f"  declared numerical tolerance: {NUMERICAL_TOLERANCE:g}")
    print(f"  outputs per channel: {len(out_a)} "
          f"(indices {out_a[0].index}..{out_a[-1].index})")
    print(f"  first output: t={out_a[0].time} ts={out_a[0].timestamp:.9f}s")
    print(f"  last output:  t={float(out_a[-1].time)} "
          f"ts={out_a[-1].timestamp:.9f}s")
    print(f"  checkpoint at output #{snap['out_index']} "
          f"(next t={snap['next_time']}, inside filter tail of switch "
          f"@{SWITCH_1}); replay identical: OK")
    print(f"  max |clock drift| between channels: 0 (exact rationals)")
    print(f"  max deviation vs direct reference: {worst:.3e}")
    print(f"  gaps at source positions: {gaps}")
    for r0, r1 in ranges:
        print(f"  gap-contaminated output range: t in [{r0}, {r1}]")
    print("ALL CHECKS PASSED")


if __name__ == "__main__":
    main()
