"""Runnable demo: static curve compile, processing, hot reload, inverse.

Run from the repo root:  python3.11 examples/demo.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from dyncurve import (COMPRESS, EXPAND, DetectorConfig, DynamicsProcessor,
                      EnvelopePolicy, Segment, SidechainConfig, SmootherConfig,
                      amp_to_db, compile_curve)
from dyncurve.fixtures import SAMPLE_RATE, multichannel_demo


def peak_db(sig):
    return amp_to_db(max((abs(x) for x in sig), default=0.0))


def main():
    # 1. Compile a multi-segment curve: downward expander + soft-knee
    #    compressor + limiter plateau.
    curve = compile_curve([
        Segment(-60.0, 2.0, knee_db=4.0, kind=EXPAND),
        Segment(-24.0, 4.0, knee_db=8.0, kind=COMPRESS),
        Segment(-6.0, float("inf"), knee_db=0.0, kind=COMPRESS),
    ])
    print("knots (dB):", [round(k, 3) for k in curve.knots])
    for x in (-80.0, -60.0, -40.0, -24.0, -12.0, -6.0, 0.0):
        print(f"  in {x:7.1f} dB -> out {curve.output_db(x):8.3f} dB "
              f"(slope {curve.slope(x):.3f})")

    # 2. Inverse lookup, incl. the non-invertible limiter plateau.
    inv = curve.invert(curve.output_db(-12.0))
    print(f"inverse of out(-12 dB): [{inv.x_lo:.6f}, {inv.x_hi:.6f}] "
          f"plateau={inv.is_plateau}")
    print("plateau regions:", [(round(a, 2), round(b, 2), round(y, 3))
                               for a, b, y in curve.plateau_regions()])

    # 3. Process a two-channel fixture (linked sidechain).
    block = multichannel_demo()
    proc = DynamicsProcessor(
        num_channels=2, sample_rate=SAMPLE_RATE, curve=curve,
        detector=DetectorConfig(kind="peak", attack_s=0.003, release_s=0.05),
        smoother=SmootherConfig(attack_s=0.005, release_s=0.1),
        sidechain=SidechainConfig(link="linked"))
    out = proc.process_block(block)
    print(f"latency: {proc.latency_samples} samples")
    for ch in range(2):
        print(f"  ch{ch}: in peak {peak_db(block[ch]):7.2f} dB, "
              f"out peak {peak_db(out[ch]):7.2f} dB")

    # 4. Hot reload with crossfade at the next block boundary; envelope
    #    state retained, latency unchanged, no gain step.
    gentler = compile_curve([Segment(-24.0, 2.0, knee_db=6.0)])
    proc.update_curve(gentler, fade_samples=256,
                      envelope_policy=EnvelopePolicy.RETAIN)
    out2 = proc.process_block(block)
    print(f"after hot reload: latency still {proc.latency_samples}, "
          f"ch0 out peak {peak_db(out2[0]):.2f} dB")

    # 5. Cascade != merged parameters.
    comp = compile_curve([Segment(-20.0, 4.0)])
    expn = compile_curve([Segment(-40.0, 2.0, kind=EXPAND)])
    x = -10.0
    cascaded = expn.output_db(comp.output_db(x))
    merged = compile_curve([Segment(-20.0, 2.0)])  # naive param merge
    print(f"cascade at {x} dB: {cascaded:.3f} dB vs "
          f"merged-params {merged.output_db(x):.3f} dB (must differ)")


if __name__ == "__main__":
    main()
