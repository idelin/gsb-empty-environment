"""Runnable demo: compile a multiband-style dB curve, process a fixture
signal, hot-swap the curve, and show inverse lookup incl. plateaus.

Run:  python3.11 examples/demo.py
"""
import math
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import fixtures
from dyncurve import (DetectorConfig, DynamicsProcessor, LATENCY_SAMPLES,
                      Segment, SidechainConfig, SmootherConfig, TOLERANCES,
                      compile_curve)
from dyncurve.envelope import amplitude_to_db


def rms(xs):
    return math.sqrt(sum(x * x for x in xs) / max(len(xs), 1))


def main():
    fs = fixtures.SAMPLE_RATE
    # multisegment curve: downward expander + soft-knee compressor + limiter
    curve = compile_curve([
        Segment(-70.0, 2.0, kind="expand"),
        Segment(-24.0, 4.0, knee_db=8.0),
        Segment(-6.0, math.inf),                      # limiter plateau
    ])
    print("== compile checks ==")
    for c in curve.checks:
        print(f"  {c.name:18s} ok={c.ok}  {c.detail}")
    print(f"declared latency: {LATENCY_SAMPLES} samples")
    print("tolerances:", {k: v for k, v in TOLERANCES.items()})

    print("\n== static gain table ==")
    for x in (-100, -80, -70, -60, -40, -28, -24, -20, -12, -6, -3, 0):
        print(f"  in {x:7.1f} dBFS -> out {curve.output_db(x):8.2f} dBFS "
              f"(gain {curve.gain_db(x):+7.2f} dB)")

    print("\n== inverse lookup ==")
    for y in (-60.0, -30.0, -7.0):
        print(f"  y={y:7.1f} dB -> x interval {curve.inverse_db(y)}")
    print("  plateaus (non-invertible):", curve.plateaus())

    print("\n== processing (2ch, linked max sidechain) ==")
    mono = fixtures.demo_signal(fs)
    block = [mono, [0.5 * s for s in mono]]
    proc = DynamicsProcessor(fs, 2, curve,
                             detector=DetectorConfig("peak", 2.0, 80.0),
                             smoother=SmootherConfig(1.0, 60.0),
                             sidechain=SidechainConfig("max"))
    out = proc.process_block(block)
    print(f"  in  rms ch0 {rms(block[0]):.4f}  ch1 {rms(block[1]):.4f}")
    print(f"  out rms ch0 {rms(out[0]):.4f}  ch1 {rms(out[1]):.4f}")
    print(f"  latency {proc.latency_samples} samples, "
          f"{proc.samples_processed} samples processed")

    print("\n== hot curve swap (crossfade at sample 4800, 480 samples) ==")
    gentler = compile_curve([Segment(-12.0, 2.0, knee_db=6.0)])
    proc2 = DynamicsProcessor(fs, 1, curve)
    proc2.set_curve(gentler, at_sample=4800, fade_samples=480)
    out2 = proc2.process_block([mono])[0]
    print(f"  out rms {rms(out2):.4f}, latency still {proc2.latency_samples} samples")
    print("  envelope state policy on swap: PRESERVED (see README/tests)")


if __name__ == "__main__":
    main()
