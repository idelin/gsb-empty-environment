"""Runnable examples for the randomized_response package (synthetic data only).

Run from the repository root:  python3.11 examples/run_examples.py
"""
import os
import random
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from randomized_response import (
    ChannelGroup,
    RandomizedResponseChannel,
    confidence_intervals,
    estimate_distribution,
    estimate_from_counts,
    exact_binary_reference,
    simulate_counts,
    theoretical_output_distribution,
)


def fmt(xs):
    return "[" + ", ".join(f"{x: .4f}" for x in xs) + "]"


def example_binary():
    print("=" * 72)
    print("1) Binary randomized response (k = 2, epsilon = 1.0)")
    ch = RandomizedResponseChannel(k=2, epsilon=1.0)
    true = [0.30, 0.70]
    counts = simulate_counts(ch, true, 20_000, random.Random(1))
    report = estimate_from_counts(ch, counts)
    ci = confidence_intervals([ChannelGroup(ch, counts)], confidence=0.95)
    print(f"   true distribution   : {fmt(true)}")
    print(f"   observed counts     : {counts}")
    print(f"   raw estimate        : {fmt(report.raw_estimate)}  (may leave [0,1])")
    print(f"   clipped estimate    : {fmt(report.estimate)}  (non-negative, sums to 1)")
    print(f"   clipped coordinates : {report.clipped}")
    print(f"   residual (wRMS)     : {report.residual:.2e}   condition number: {report.condition_number:.2f}")
    print(f"   95% Wald intervals  : {[(f'{lo:.3f}', f'{hi:.3f}') for lo, hi in ci.intervals]}")
    print(f"   coverage assumptions: {ci.assumptions}")


def example_heterogeneous():
    print("=" * 72)
    print("2) Heterogeneous known channels, joint recovery (k = 4)")
    true = [0.10, 0.20, 0.30, 0.40]
    groups = []
    for eps, n, seed in ((0.5, 30_000, 10), (1.0, 30_000, 11), (2.0, 30_000, 12)):
        ch = RandomizedResponseChannel(k=4, epsilon=eps)
        groups.append(ChannelGroup(ch, simulate_counts(ch, true, n, random.Random(seed))))
    report = estimate_distribution(groups)
    print(f"   true distribution   : {fmt(true)}")
    print(f"   3 channel groups    : epsilon = 0.5 / 1.0 / 2.0, n = 30,000 each")
    print(f"   joint raw estimate  : {fmt(report.raw_estimate)}")
    print(f"   joint clipped est.  : {fmt(report.estimate)}")
    print(f"   identifiable        : {report.identifiable}")
    print(f"   residual (wRMS)     : {report.residual:.2e}   condition number: {report.condition_number:.2f}")


def example_epsilon0():
    print("=" * 72)
    print("3) epsilon = 0: output independent of input -> NOT identifiable")
    ch = RandomizedResponseChannel(k=3, epsilon=0.0)
    counts = simulate_counts(ch, [0.6, 0.3, 0.1], 10_000, random.Random(2))
    report = estimate_from_counts(ch, counts)
    print(f"   output frequencies  : {fmt([c / sum(counts) for c in counts])} (uniform, input-independent)")
    print(f"   identifiable        : {report.identifiable}")
    print(f"   null directions     : {len(report.null_directions)} (not inverted; minimum-norm solution reported)")
    print(f"   raw estimate        : {fmt(report.raw_estimate)}")
    for note in report.notes:
        print(f"   note: {note}")


def example_k1():
    print("=" * 72)
    print("4) k = 1 special case")
    ch = RandomizedResponseChannel(k=1, epsilon=2.0)
    report = estimate_from_counts(ch, [500])
    print(f"   channel matrix      : {ch.matrix}  (output carries no information)")
    print(f"   estimate            : {report.estimate}  (trivially the only distribution)")
    for note in report.notes:
        print(f"   note: {note}")


def example_frequencies_vs_theory():
    print("=" * 72)
    print("5) Simulated frequencies vs theoretical probabilities (k = 3, eps = 1.0)")
    ch = RandomizedResponseChannel(k=3, epsilon=1.0)
    true = [0.2, 0.5, 0.3]
    n = 100_000
    counts = simulate_counts(ch, true, n, random.Random(3))
    theo = theoretical_output_distribution(ch, true)
    print(f"   true distribution   : {fmt(true)}")
    print(f"   theoretical output  : {fmt(theo)}")
    print(f"   simulated frequency : {fmt([c / n for c in counts])}")


def example_exact_reference():
    print("=" * 72)
    print("6) Exact small-domain reference: bias of raw vs clipped estimator")
    eps, x0, n = 0.8, 0.97, 20
    ref = exact_binary_reference(eps, x0, n)
    print(f"   binary RR, epsilon={eps}, true P(0)={x0}, n={n}")
    print(f"   exact E[raw]        : {ref.mean_raw:.6f}  (unbiased, equals truth)")
    print(f"   exact sd[raw]       : {ref.var_raw ** 0.5:.6f}")
    print(f"   exact bias[clipped] : {ref.bias_clipped:+.6f}  (clipping pulls toward interior)")


if __name__ == "__main__":
    example_binary()
    example_heterogeneous()
    example_epsilon0()
    example_k1()
    example_frequencies_vs_theory()
    example_exact_reference()
