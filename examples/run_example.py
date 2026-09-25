"""Runnable demonstration of the verifier.

Usage (from the repository root, Python 3.11):
    python3.11 -m examples.run_example

Verifies a known-good streaming FIR, then injects seven known-bad
implementations and shows that each is detected.  Replayable fixtures,
reports and shrunk counterexamples are written to ./artifacts/.
"""
from __future__ import annotations

import os

from verifier import (FilterSpec, verify, StreamingFIR, design_lowpass,
                      generate_fixtures)
from verifier import bad_impls
from verifier.streaming import ChunkCounterexample, ShiftCounterexample

SPEC = FilterSpec(
    sample_rate=48000.0,
    passband=(0.0, 8000.0),
    stopband=(12000.0, 24000.0),
    passband_ripple_db=0.5,
    stopband_atten_db=40.0,
)


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    art = os.path.join(root, "artifacts")
    os.makedirs(art, exist_ok=True)

    h_good = design_lowpass(101, 10000.0, SPEC.sample_rate)
    fixtures = generate_fixtures(SPEC.sample_rate, SPEC.passband, SPEC.stopband)
    fixtures.save(os.path.join(art, "fixtures.json"))

    print("=" * 76)
    print("GOOD IMPLEMENTATION: 101-tap blackman lowpass, fc = 10 kHz, fs = 48 kHz")
    print("=" * 76)
    good_dir = os.path.join(art, "good")
    report = verify(lambda: StreamingFIR(h_good), SPEC,
                    fir_coefficients=h_good, fixtures=fixtures,
                    chunk_trials=12, artifact_dir=good_dir)
    print(report.format_text())
    os.makedirs(good_dir, exist_ok=True)
    report.save(os.path.join(good_dir, "report.json"))

    bads = [
        ("state_reset", lambda: bad_impls.StateResetFIR(h_good)),
        ("chunk_delay", lambda: bad_impls.ChunkDelayFIR(h_good)),
        ("absolute_index", lambda: bad_impls.AbsoluteIndexFIR(h_good, period=257)),
        ("gain_error", lambda: bad_impls.GainErrorFIR(h_good, 0.5)),
        ("latency_liar", lambda: bad_impls.LatencyLiarFIR(h_good)),
        ("underspec_stopband", lambda: bad_impls.make_underspec_stopband(SPEC)),
        ("ripply_passband", lambda: bad_impls.make_ripply_passband(SPEC)),
    ]
    print()
    print("=" * 76)
    print("INJECTED KNOWN-BAD IMPLEMENTATIONS (detection check)")
    print("=" * 76)
    for name, factory in bads:
        dest = os.path.join(art, f"bad_{name}")
        rep = verify(factory, SPEC, fixtures=fixtures, chunk_trials=12,
                     artifact_dir=dest)
        os.makedirs(dest, exist_ok=True)
        rep.save(os.path.join(dest, "report.json"))
        failed = [c.name for c in rep.checks if not c.passed]
        status = "DETECTED" if failed else "MISSED!"
        print(f"  {name:20s} {status:9s} failed checks: {', '.join(failed)}")

    print()
    print("=" * 76)
    print("REPLAY OF SHRUNK COUNTEREXAMPLES")
    print("=" * 76)
    cex_path = os.path.join(art, "bad_state_reset", "chunk_counterexample.json")
    cex = ChunkCounterexample.load(cex_path)
    fails_bad, diff_bad = cex.still_fails(lambda: bad_impls.StateResetFIR(h_good))
    fails_good, diff_good = cex.still_fails(lambda: StreamingFIR(h_good))
    print(f"  chunk counterexample: input={len(cex.input)} samples, "
          f"boundaries={cex.boundaries}, tol={cex.tolerance:g}")
    print(f"    replay vs StateResetFIR : still_fails={fails_bad} "
          f"max_abs_diff={diff_bad:.3g}")
    print(f"    replay vs good filter   : still_fails={fails_good} "
          f"max_abs_diff={diff_good:.3g}")
    sex_path = os.path.join(art, "bad_absolute_index", "shift_counterexample.json")
    sex = ShiftCounterexample.load(sex_path)
    fails_shift, diff_shift = sex.still_fails(
        lambda: bad_impls.AbsoluteIndexFIR(h_good, period=257))
    print(f"  shift counterexample: shift={sex.shift} sample(s), "
          f"input={len(sex.input)} samples")
    print(f"    replay vs AbsoluteIndexFIR: still_fails={fails_shift} "
          f"max_abs_diff={diff_shift:.3g}")
    print()
    print(f"artifacts written under {art}")


if __name__ == "__main__":
    main()
