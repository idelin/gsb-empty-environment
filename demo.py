"""Run example: subject-level counts on SYNTHETIC data only.

Run:  python3.11 demo.py
"""

from fractions import Fraction
from random import Random, randint

from dpcount import (
    DiscreteLaplaceCount,
    add_laplace_noise,
    rational_decay_for_epsilon,
    verify_dp,
)


def section(title):
    print()
    print("=" * 72)
    print(title)
    print("=" * 72)


def main():
    # ------------------------------------------------------------------
    # 1. Synthetic data: a fixed synthetic population of N subjects, each
    #    carrying synthetic 0/1 attributes.  No real data is used anywhere.
    # ------------------------------------------------------------------
    rng = Random(20260926)
    n_subjects = 12
    groups = {
        "synthetic_group_A": [rng.randint(0, 1) for _ in range(n_subjects)],
        "synthetic_group_B": [rng.randint(0, 1) for _ in range(n_subjects)],
    }
    true_counts = {name: sum(v) for name, v in groups.items()}

    section("1. Synthetic subject-level counts (query: count of flag == 1)")
    print("population size:", n_subjects)
    for name, c in true_counts.items():
        print("  %-20s true count = %d" % (name, c))
    print("subject sensitivity Delta = 1 "
          "(add/remove one subject changes a count by at most 1)")

    epsilon_target = 1.0
    # Truncation to a finite output domain changes the normalizers and can
    # push the true bound slightly above the unbounded-domain value, so pick
    # the decay from a slightly smaller budget and CONFIRM with verify_dp.
    choose_budget = 0.99

    # ------------------------------------------------------------------
    # 2. Exact rational discrete mechanism
    # ------------------------------------------------------------------
    section("2. Discrete Laplace (two-sided geometric), exact rational probs")
    decay = rational_decay_for_epsilon(choose_budget, sensitivity=1,
                                       precision_bits=32)
    lo, hi = -4, n_subjects + 4
    mech = DiscreteLaplaceCount(sensitivity=1, decay=decay, lo=lo, hi=hi)
    print("output domain X = [%d, %d]" % (lo, hi))
    print("decay p = %s (dyadic rational, 32 bits)" % decay)
    print("(p chosen from budget %.2f so the VERIFIED epsilon stays <= %.2f "
          "despite truncation)" % (choose_budget, epsilon_target))

    print("\n-- raw vs post-processed (5 runs per group, fixed seed) --")
    print("(raw values may be negative; they are NEVER silently redrawn)")
    fixed_seeds = {"synthetic_group_A": 101, "synthetic_group_B": 202}
    for name, c in true_counts.items():
        local = Random(fixed_seeds[name])
        for _ in range(5):
            rec = mech.noisy_count(c, local)
            print("  %-20s c=%2d  raw=%3d  postprocessed=%3d"
                  % (name, rec.true_count, rec.raw, rec.postprocessed))

    # ------------------------------------------------------------------
    # 3. Deterministic inverse CDF replay
    # ------------------------------------------------------------------
    section("3. Deterministic inverse CDF (recompute output from given input)")
    print("M: Pr[M(c)=x] = p^|x-c| / Z(c), inverse CDF at explicit rational u")
    for u in (Fraction(0), Fraction(1, 3), Fraction(7, 10),
              Fraction(999, 1000)):
        x = mech.inverse_cdf(true_counts["synthetic_group_A"], u)
        print("  c=%d, u=%s -> M(c) = %d"
              % (true_counts["synthetic_group_A"], u, x))
    print("same rational u always gives the same integer (fully replayable)")

    # ------------------------------------------------------------------
    # 4. Formal exhaustive verification over the finite adjacency domain
    # ------------------------------------------------------------------
    section("4. Formal verification: enumerate all (c, c', x) with exact math")
    print("this is a finite exhaustive proof, NOT a sampling-frequency test")
    report = verify_dp(mech, count_lo=0, count_hi=n_subjects)
    print(report)
    naive = Fraction(1) / decay  # unbounded-domain bound p^-Delta
    holds = report.max_ratio <= naive
    print("naive unbounded bound 1/p = %s : %s"
          % (naive, "holds here" if holds else "does NOT hold (truncation)"))
    print("conclusion: mechanism is epsilon-DP with epsilon = ln(max ratio);")
    print("verified epsilon %.6f <= target %.2f: %s"
          % (report.verified_epsilon, epsilon_target,
             report.verified_epsilon <= epsilon_target))

    # ------------------------------------------------------------------
    # 5. Continuous Laplace: original interface, NUMERICAL APPROXIMATION
    # ------------------------------------------------------------------
    section("5. Continuous Laplace interface (float; NUMERICAL APPROXIMATION)")
    crng = Random(4242)
    for name, c in list(true_counts.items()):
        rec = add_laplace_noise(c, sensitivity=1, epsilon=1.0, rng=crng)
        print("  %-20s u=%.6f  raw_noise=%+9.4f  raw=%9.4f  post=%g"
              % (name, rec.uniform_input, rec.raw_noise,
                 rec.raw_noisy_count, rec.postprocessed_count))
    print("epsilon <= 0 is rejected; u == 0/1 raises rather than resampling")

    # ------------------------------------------------------------------
    # 6. Statistical experiment (diagnostic ONLY, not a privacy proof)
    # ------------------------------------------------------------------
    section("6. Statistical experiment (diagnostic only; NOT a privacy proof)")
    trials = 40000
    small = DiscreteLaplaceCount(1, Fraction(1, 2), -3, 3)
    srng = Random(2026)
    freq = {x: 0 for x in range(-3, 4)}
    for _ in range(trials):
        freq[small.sample(0, srng)] += 1
    print("discrete sampler, %d trials, c=0, p=1/2, X=[-3,3]:" % trials)
    worst = 0.0
    for x in range(-3, 4):
        observed = freq[x] / trials
        exact = float(small.probability(x, 0))
        worst = max(worst, abs(observed - exact))
        print("  x=%2d  observed=%.5f  exact=%.5f" % (x, observed, exact))
    print("max |observed - exact| = %.5f" % worst)
    print("NOTE: empirical frequencies never prove DP; the formal guarantee "
          "is section 4.")


if __name__ == "__main__":
    main()
