#!/usr/bin/env python3
"""End-to-end demo of the fractional-delay alignment engine.

Covers: integer + fractional delays, smooth mid-stream delay ramps, a
checkpoint/retract of the uncommitted region, missing-data contamination,
the common valid interval with zero-pad markers, tail flushing and the
derived memory bound. All samples come from the local fixture (no audio
device); delays and the numeric tolerance are declared up front.
"""
from __future__ import annotations

import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from fractional_align import fixture
from fractional_align.engine import CONTAMINATED, ZERO_PAD, Aligner
from fractional_align.reference import render_channel

BLOCK = 24
COMMIT_SPAN = 240


def main() -> int:
    length = 1024
    inputs = {
        "impulse": fixture.impulse(length, 200),
        "sweep": fixture.swept_sine(length, 100.0, 4000.0),
    }
    missing = {"sweep": [(700, 708)]}

    al = Aligner(
        sample_rate=fixture.SAMPLE_RATE,
        coefficients=fixture.fractional_coefficients,
        kernel_length=fixture.KERNEL_LENGTH,
        max_delay=fixture.MAX_DELAY,
    )
    al.add_channel("impulse", initial_delay=33.5)  # integer + fractional delay
    al.add_channel("sweep", initial_delay=8.0)
    al.declare_delay("sweep", 512, 24.0)           # smooth ramp up
    al.declare_delay("sweep", 900, 10.0)           # smooth ramp back down
    al.declare_missing("sweep", missing["sweep"])

    for name, samples in inputs.items():
        al.feed(name, samples)
        al.finish(name)

    blocks = []
    while al.cursor < 240:
        blocks.append(al.process(BLOCK))

    # checkpoint, run ahead, then retract the uncommitted region and replay
    checkpoint = al.checkpoint()
    ahead = [al.process(BLOCK) for _ in range(3)]
    al.restore(checkpoint)
    again = [al.process(BLOCK) for _ in range(3)]
    identical = all(a.data == b.data and a.flags == b.flags
                    for a, b in zip(ahead, again))
    blocks.extend(again)

    high_water = 0
    while al.cursor < 480:
        blocks.append(al.process(BLOCK))
        if al.cursor - al.committed >= COMMIT_SPAN:
            al.commit()
        high_water = max(high_water,
                         max(al.retained_history(n) for n in inputs))
    blocks.extend(al.flush(BLOCK))

    out = {name: ([], []) for name in inputs}
    expected_start = 0
    contiguous = True
    for block in blocks:
        contiguous &= block.start == expected_start
        expected_start = block.start + block.length
        for name in inputs:
            out[name][0].extend(block.data[name])
            out[name][1].extend(block.flags[name])

    worst = 0.0
    flags_ok = True
    for name, samples in inputs.items():
        ref_data, ref_flags = render_channel(
            samples, al.schedule(name), missing.get(name, ()),
            fixture.fractional_coefficients, fixture.KERNEL_LENGTH,
            0, len(out[name][0]),
        )
        worst = max(worst, max(abs(a - b)
                               for a, b in zip(out[name][0], ref_data)))
        flags_ok &= list(out[name][1]) == list(ref_flags)

    print(f"sample rate          : {fixture.SAMPLE_RATE:.0f} Hz (fixture, no audio device)")
    print(f"declared tolerance   : {fixture.TOLERANCE:.0e}")
    print(f"kernel / max delay   : {fixture.KERNEL_LENGTH} taps / {fixture.MAX_DELAY:.0f} samples")
    print(f"channels             : {', '.join(inputs)} ({length} samples each)")
    print(f"common time base     : outputs 0..{al.cursor - 1}, blocks contiguous: {contiguous}")
    lo, hi = al.common_valid_interval()
    print(f"common valid interval: [{lo}, {hi}]")
    for name in inputs:
        _, flags = out[name]
        pads = sum(1 for f in flags if f & ZERO_PAD)
        contam = sum(1 for f in flags if f & CONTAMINATED)
        print(f"  {name:<8} zero-padded={pads:<4} contaminated={contam}")
    data, _ = out["impulse"]
    peak = max(range(len(data)), key=lambda i: abs(data[i]))
    print(f"impulse peak         : output sample {peak} "
          f"(input 200 + declared delay 33.5 = target 233.5)")
    print(f"checkpoint replay    : identical re-emission after retract: {identical}")
    print(f"engine vs reference  : max |diff| = {worst:.3e} "
          f"(tolerance {fixture.TOLERANCE:.0e}), flags match: {flags_ok}")
    bound = al.memory_bound(COMMIT_SPAN + BLOCK)
    print(f"retained history     : high-water {high_water} samples "
          f"(bound = uncommitted span + ceil(max delay) + kernel = {bound})")

    ok = (identical and contiguous and flags_ok
          and worst <= fixture.TOLERANCE and high_water <= bound)
    print("RESULT:", "PASS" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
