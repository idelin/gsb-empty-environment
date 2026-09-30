"""Runnable demo of the subsample_amp package (synthetic parameters only).

Run from the repository root:

    python3 examples/run_examples.py

Everything printed here is arithmetic evidence about the formula
eps' = log(1 + q*(exp(eps) - 1)); it is not a privacy proof.
"""

import math
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from subsample_amp import (  # noqa: E402
    NOT_A_PRIVACY_PROOF,
    SYNTHETIC_DATA_ONLY,
    ApplicabilityError,
    Const,
    Param,
    SubsampleAmp,
    compose_rounds,
    max_q_for_budget,
    reference_amp,
    subsampled_epsilon,
)


def section(title):
    print()
    print("=" * 72)
    print(title)
    print("=" * 72)


def main():
    print("subsample_amp demo -- synthetic parameters only")
    print(SYNTHETIC_DATA_ONLY)
    print(NOT_A_PRIVACY_PROOF)

    section("1. Stable evaluation of eps' = log(1 + q*(exp(eps) - 1))")
    cases = [
        (1.0, 0.0, "q = 0 boundary (exact 0)"),
        (1.0, 1.0, "q = 1 boundary (exact eps)"),
        (1.0, 0.01, "moderate"),
        (0.1, 0.5, "moderate"),
        (2.0, 1e-9, "tiny q (first-order regime)"),
        (1000.0, 0.01, "eps beyond float exp range"),
        (1000.0, 1e-300, "huge eps, tiny q"),
        (1e300, 0.25, "astronomical eps, no overflow"),
    ]
    for eps, q, note in cases:
        print(f"  eps={eps:<8} q={q:<8} -> {subsampled_epsilon(eps, q)!r:<24} # {note}")

    section("2. Expression DAG with outward-rounded interval evaluation")
    node = SubsampleAmp(Param("eps"), Param("q"))
    for eps, q in ((1.0, 0.01), (1000.0, 0.01)):
        iv = node.evaluate({"eps": eps, "q": q})
        lo_f, hi_f = iv.to_float()
        print(f"  eps={eps}, q={q}")
        print(f"    decimal interval : [{iv.lo}, {iv.hi}]")
        print(f"    float hull       : [{lo_f!r}, {hi_f!r}]")
        print(f"    float value      : {subsampled_epsilon(eps, q)!r} (inside hull)")
        print(f"    series reference : {reference_amp(eps, q)} (inside interval: {iv.contains(reference_amp(eps, q))})")

    section("3. Multi-round composition (basic composition, sum of eps')")
    rounds = [(0.5, 0.1)] * 10
    total = compose_rounds(rounds).evaluate()
    per_round = subsampled_epsilon(0.5, 0.1)
    print(f"  10 rounds of (eps=0.5, q=0.1): per-round eps' = {per_round!r}")
    print(f"  composed interval: [{total.lo}, {total.hi}]")
    print(f"  composed float hull: {total.to_float()}")
    heavy = compose_rounds([(1000.0, 1e-4)] * 100).evaluate()
    print(f"  100 rounds of (eps=1000, q=1e-4): float hull {heavy.to_float()} (no overflow)")

    section("4. Inversion: largest q for a target budget, with evidence")
    for eps, target in ((1.0, 0.3), (5.0, 1.0), (1000.0, 5.0), (2.0, 2.0), (2.0, 0.0)):
        res = max_q_for_budget(eps, target)
        print(f"  --- eps={eps}, target={target}")
        for line in res.evidence().splitlines():
            print(f"      {line}")

    section("5. Applicability enforcement (formula rejected outside its model)")
    rejections = [
        ("q = 1.5", lambda: subsampled_epsilon(1.0, 1.5)),
        ("eps = -0.1", lambda: subsampled_epsilon(-0.1, 0.5)),
        ("model = 'uniform_without_replacement'",
         lambda: subsampled_epsilon(1.0, 0.5, model="uniform_without_replacement")),
        ("base_delta = 1e-5",
         lambda: subsampled_epsilon(1.0, 0.5, base_delta=1e-5)),
        ("compose with invalid round",
         lambda: compose_rounds([(0.5, 0.1), (1.0, 2.0)])),
    ]
    for label, thunk in rejections:
        try:
            thunk()
        except ApplicabilityError as exc:
            print(f"  rejected {label}: {exc}")
        else:
            print(f"  NOT rejected (bug!): {label}")

    section("6. Boundary cross-check against the independent series reference")
    for eps, q in ((1e-3, 1e-6), (1.0, 0.999), (700.0, 0.25), (1000.0, 1e-300)):
        ref = reference_amp(eps, q)
        val = subsampled_epsilon(eps, q)
        iv = SubsampleAmp(Const(eps), Const(q)).evaluate()
        print(f"  eps={eps:<8} q={q:<8} float={val!r}")
        print(f"    reference in interval: {iv.contains(ref)}, |float-ref|/|ref| = {abs(val - float(ref)) / abs(float(ref)):.2e}")

    print()
    print("Reminder:", NOT_A_PRIVACY_PROOF)


if __name__ == "__main__":
    main()
