"""Runnable demo: certified-envelope rejection sampling on a finite domain."""

import random
import threading

from rejection_sampling import (
    EnvelopeViolationError,
    PartitionedEnvelope,
    RejectionSampler,
    check_rejection_counts,
    exact_finite_distribution,
    expected_acceptance_probability,
    verify_sample_distribution,
)

DOMAIN = list(range(10))
WEIGHTS = [1, 2, 3, 4, 5, 5, 4, 3, 2, 1]  # unnormalized target, total = 30


def target(x):
    return WEIGHTS[x]


def proposal_pdf(x):
    return 0.1  # uniform over DOMAIN


def proposal_sample(rng):
    return rng.randrange(10)


def main():
    print("=== 1. Certified envelope with adaptive refinement ===")
    env = PartitionedEnvelope(target, proposal_pdf)
    env.add_partition(0.0, 10.0, 60.0, certificate={"note": "loose caller bound"})
    # Adaptively refine into two halves, each exactly verified over the finite domain.
    certs = env.refine(
        0.0,
        10.0,
        [
            {"lo": 0.0, "hi": 5.0, "bound": 50.0, "exact_domain": DOMAIN},
            {"lo": 5.0, "hi": 10.0, "bound": 50.0, "exact_domain": DOMAIN},
        ],
    )
    for c in certs:
        print(f"  partition {c.partition}: bound={c.bound} method={c.method} "
              f"max_ratio={c.evidence['max_ratio']}")

    print("\n=== 2. Sampling with injected RNG (seed recorded) ===")
    seed = 20260927
    sampler = RejectionSampler(
        target=target,
        proposal_pdf=proposal_pdf,
        proposal_sample=proposal_sample,
        envelope=env,
        rng=random.Random(seed),
        seed=seed,
    )
    result = sampler.sample(2000, budget=100_000)
    print(f"  reason={result.reason} seed={result.seed} "
          f"accepted={result.accepted} attempts={result.attempts}")
    print(f"  empirical acceptance rate = {result.acceptance_rate:.4f} "
          f"(denominator = attempts)")

    print("\n=== 3. Exact vs empirical (labelled separately) ===")
    exact = exact_finite_distribution(target, DOMAIN)
    print(f"  [{exact['label']}] probabilities:",
          {x: float(p) for x, p in sorted(exact['probabilities'].items())})
    check = verify_sample_distribution(result.samples, target, DOMAIN, tolerance=0.05)
    emp = check["empirical"]
    print(f"  [{emp['label']}] n={emp['n']} probabilities:",
          {x: round(p, 4) for x, p in sorted(emp['probabilities'].items())})
    print(f"  max deviation={check['max_deviation']:.4f} "
          f"within_tolerance={check['within_tolerance']}")

    print("\n=== 4. Rejection-count verification ===")
    expected = expected_acceptance_probability(target, proposal_pdf, DOMAIN, 50.0)
    report = check_rejection_counts(result, expected, tolerance=0.05)
    print(f"  exact expected acceptance = {float(report.expected_acceptance):.4f}")
    print(f"  empirical observed = {report.observed_acceptance:.4f} "
          f"(accepted={report.accepted}, rejected={report.rejected}, "
          f"attempts={report.attempts})")
    print(f"  within_tolerance={report.within_tolerance}")

    print("\n=== 5. Budget exhaustion returns partial progress ===")
    tight = sampler.sample(500, budget=8)
    print(f"  reason={tight.reason} accepted={tight.accepted} "
          f"attempts={tight.attempts} remaining={tight.remaining}")

    print("\n=== 6. Cancellation ===")
    event = threading.Event()
    count = {"n": 0}

    def counting_proposal(rng):
        count["n"] += 1
        if count["n"] >= 25:
            event.set()
        return proposal_sample(rng)

    cancellable = RejectionSampler(
        target=target, proposal_pdf=proposal_pdf,
        proposal_sample=counting_proposal, envelope=env,
        rng=random.Random(1), seed=1,
    )
    stopped = cancellable.sample(500, cancel=event)
    print(f"  reason={stopped.reason} accepted={stopped.accepted} "
          f"attempts={stopped.attempts} remaining={stopped.remaining}")

    print("\n=== 7. Envelope violation aborts (never clamped to 1) ===")
    bad_env = PartitionedEnvelope(target, proposal_pdf)
    bad_env.add_partition(0.0, 10.0, 40.0, certificate={"false": "bound too small"})
    bad = RejectionSampler(
        target=target, proposal_pdf=proposal_pdf,
        proposal_sample=proposal_sample, envelope=bad_env,
        rng=random.Random(7), seed=7,
    )
    try:
        bad.sample(100, budget=10_000)
    except EnvelopeViolationError as exc:
        print(f"  EnvelopeViolationError: {exc}")

    print("\n=== 8. Checkpoint / restore reproducibility ===")
    ref = RejectionSampler(
        target=target, proposal_pdf=proposal_pdf,
        proposal_sample=proposal_sample, envelope=env,
        rng=random.Random(42), seed=42,
    )
    full = ref.sample(10, budget=10_000)

    first = RejectionSampler(
        target=target, proposal_pdf=proposal_pdf,
        proposal_sample=proposal_sample, envelope=env,
        rng=random.Random(42), seed=42,
    )
    part1 = first.sample(4, budget=10_000)
    checkpoint = first.state
    print(f"  checkpoint: attempts={checkpoint.attempts} "
          f"accepted_positions={checkpoint.accepted_positions}")

    resumed = RejectionSampler(
        target=target, proposal_pdf=proposal_pdf,
        proposal_sample=proposal_sample, envelope=env,
        rng=random.Random(0), seed=42,
    )
    resumed.restore(checkpoint)
    part2 = resumed.sample(6, budget=10_000)
    match = part1.samples + part2.samples == full.samples
    print(f"  split-run matches uninterrupted run: {match}")
    print(f"  recovered accepted positions: {resumed.accepted_positions}")


if __name__ == "__main__":
    main()
