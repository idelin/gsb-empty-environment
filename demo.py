"""Runnable demonstration on synthetic data (python3 demo.py).

Scenarios:
  1. Binary randomized response (epsilon-parameterized), single group.
  2. k-ary (k = 4) randomized response, single group.
  3. Heterogeneous participants: two groups with different known channels,
     joint recovery of the population distribution.
  4. Degenerate channel epsilon = 0: output independent of input, inverse
     estimation unidentifiable (no inverse is attempted).
  5. k = 1 special case.
  6. Exact small-domain reference: bias/variance check without Monte Carlo.
"""

import random

import exact
import randomized_response as rr
from estimate import ChannelGroup, estimate_distribution


def fmt(vec):
    return "[" + ", ".join(f"{v:+.4f}" for v in vec) + "]"


def show_result(result, truth):
    print(f"  identifiable        : {result.identifiable}")
    print(f"  condition number    : {result.condition_number:.3f}")
    print(f"  true distribution   : {fmt(truth)}")
    if result.pi_unconstrained is not None:
        print(f"  unconstrained est.  : {fmt(result.pi_unconstrained)}")
    if result.pi_clipped is not None:
        print(f"  clipped est.        : {fmt(result.pi_clipped)}")
    if result.pi_constrained is not None:
        print(f"  constrained est.    : {fmt(result.pi_constrained)}")
    if result.ci95 is not None:
        for i, (lo, hi) in enumerate(result.ci95):
            print(f"  95% CI component {i}  : ({lo:+.4f}, {hi:+.4f})")
    for name, res in result.residuals.items():
        pad = " " * max(0, 8 - len(name))
        print(f"  residual [{name}]{pad}: {fmt(res)}")
    for note in result.notes:
        print(f"  note: {note}")


def main():
    rng = random.Random(20261001)

    print("=== 1. Binary randomized response, epsilon = 1.0, n = 20000 ===")
    pi = [0.3, 0.7]
    P = rr.binary_channel(epsilon=1.0)
    counts = rr.simulate(rng, pi, P, 20000)
    q = rr.output_probabilities(P, pi)
    print(f"  theoretical output q = {fmt(q)}")
    print(f"  simulated freq       = {fmt([c / 20000 for c in counts])}")
    show_result(estimate_distribution([ChannelGroup("bin", P, counts)]), pi)

    print("\n=== 2. k-ary randomized response, k = 4, epsilon = 1.5, n = 40000 ===")
    pi = [0.4, 0.3, 0.2, 0.1]
    P = rr.kary_channel(4, 1.5)
    counts = rr.simulate(rng, pi, P, 40000)
    q = rr.output_probabilities(P, pi)
    print(f"  theoretical output q = {fmt(q)}")
    print(f"  simulated freq       = {fmt([c / 40000 for c in counts])}")
    show_result(estimate_distribution([ChannelGroup("k4", P, counts)]), pi)

    print("\n=== 3. Heterogeneous channels, joint recovery ===")
    pi = [0.5, 0.3, 0.2]
    P1 = rr.kary_channel(3, 0.5)
    P2 = rr.kary_channel(3, 2.0)
    g1 = ChannelGroup("eps0.5", P1, rr.simulate(rng, pi, P1, 15000))
    g2 = ChannelGroup("eps2.0", P2, rr.simulate(rng, pi, P2, 15000))
    show_result(estimate_distribution([g1, g2]), pi)

    print("\n=== 4. Degenerate channel epsilon = 0 (k = 3, n = 10000) ===")
    P0 = rr.kary_channel(3, 0.0)
    counts_a = rr.simulate(rng, [0.9, 0.05, 0.05], P0, 10000)
    counts_b = rr.simulate(rng, [0.05, 0.05, 0.9], P0, 10000)
    print(f"  freq under pi=[.9,.05,.05] : {fmt([c / 10000 for c in counts_a])}")
    print(f"  freq under pi=[.05,.05,.9] : {fmt([c / 10000 for c in counts_b])}")
    print("  -> output (nearly) independent of input, as theory predicts")
    result = estimate_distribution([ChannelGroup("deg", P0, counts_a)])
    print(f"  identifiable        : {result.identifiable}")
    print(f"  condition number    : {result.condition_number}")
    for d in result.null_directions:
        print(f"  unidentifiable dir  : {fmt(d)}")
    for note in result.notes:
        print(f"  note: {note}")

    print("\n=== 5. k = 1 special case ===")
    P1x1 = rr.kary_channel(1, 3.0)
    counts = rr.simulate(rng, [1.0], P1x1, 500)
    print(f"  channel = {P1x1}, counts = {counts} (output is constant)")
    show_result(estimate_distribution([ChannelGroup("k1", P1x1, counts)]), [1.0])

    print("\n=== 6. Exact small-domain reference (k = 2, eps = 1.0, n = 12) ===")
    pi = [0.25, 0.75]
    P = rr.binary_channel(epsilon=1.0)
    moments = exact.exact_estimator_moments(P, pi, 12)
    print(f"  total probability mass : {moments['total_mass']:.12f}")
    print(f"  exact mean of est.     : {fmt(moments['mean'])}")
    print(f"  exact bias             : {fmt(moments['bias'])}")
    print(f"  exact variance         : {fmt(moments['variance'])}")
    res = estimate_distribution([ChannelGroup("ref", P, [3, 9])])
    print(f"  asymptotic variance    : "
          f"{fmt([res.covariance[i][i] for i in range(2)])} (plug-in, n=12)")


if __name__ == "__main__":
    main()
