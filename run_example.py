"""Run the verifier against a good FIR and injected bad implementations.

Usage: python3 run_example.py
Writes replayable counterexample evidence to artifacts/.
"""
from __future__ import annotations

import json
import os

from filterverify import BandSpec, Tolerances, VerifierConfig, verify
from filterverify.impls import (
    AttenuatingFilter,
    BlockParityFilter,
    BlockResetFIR,
    FIRFilter,
    UndeclaredDelayFIR,
    design_lowpass,
)

FS = 8000.0
SPEC = BandSpec(sample_rate=FS, passband=(100.0, 1000.0), stopband=(1800.0, 3900.0))
TAPS = design_lowpass(num_taps=53, cutoff_hz=1400.0, fs=FS)
CFG = VerifierConfig(tolerances=Tolerances(), block_trials=12)


def main() -> None:
    os.makedirs("artifacts", exist_ok=True)
    cases = [
        ("GoodLowpass (reference)", lambda: FIRFilter(TAPS, name="GoodLowpass")),
        ("BlockResetFIR (drops state per block)", lambda: BlockResetFIR(TAPS)),
        ("UndeclaredDelayFIR (latency off by 3)", lambda: UndeclaredDelayFIR(TAPS)),
        ("AttenuatingFilter (gain 0.5)", AttenuatingFilter),
        ("BlockParityFilter (block-size-dependent gain)", BlockParityFilter),
    ]
    for label, factory in cases:
        report = verify(factory, SPEC, CFG, name=label)
        print(report.to_text())
        print("-" * 78)
        slug = label.split(" ")[0]
        with open(f"artifacts/report_{slug}.json", "w") as fh:
            fh.write(report.to_json())
        for i, ce in enumerate(report.counterexamples):
            path = f"artifacts/counterexample_{slug}_{i}.json"
            with open(path, "w") as fh:
                fh.write(ce.to_json())
            still_failing, err = ce.replay(factory)
            print(f"  replayed {path}: still_failing={still_failing}, max_error={err:.3g}")
        print()


if __name__ == "__main__":
    main()
