#!/usr/bin/env python3.11
"""Demo: empirically audit two mechanisms on synthetic neighboring datasets.

Everything here is synthetic. The audit is a statistical experiment and is
NOT a formal privacy proof. All parameters (eps, event, trials, alpha,
seed) are printed so every result is reproducible.
"""

from dp_audit import audit_mechanism, deterministic_identity, randomized_response

EPS_CLAIMED = 1.0
TRIALS = 100_000
ALPHA = 0.05
SEED = 20260928
EVENT_DESC = "S = {output == 1}"


def rr_half(record, eps, rng):
    """randomized_response tuned to 0.5-DP, audited against a 1.0 claim.

    Auditing exactly at the boundary (mechanism eps == claimed eps) leaves
    the ratio interval straddling e^eps; a strictly better mechanism gives
    a clean 'consistent' verdict.
    """
    return randomized_response(record, 0.5, rng)


def main():
    print("=" * 72)
    print("Empirical DP audit (synthetic data only; NOT a formal privacy proof)")
    print("=" * 72)

    print("\n[1] 0.5-DP mechanism audited against claim eps=%.1f" % EPS_CLAIMED)
    ok = audit_mechanism(
        rr_half,
        eps_claimed=EPS_CLAIMED,
        event=lambda out: out == 1,
        event_description=EVENT_DESC,
        trials_per_dataset=TRIALS,
        alpha=ALPHA,
        seed=SEED,
    )
    print(ok.render())

    print("\n[2] broken mechanism: deterministic_identity, claims eps=%.1f"
          % EPS_CLAIMED)
    bad = audit_mechanism(
        deterministic_identity,
        eps_claimed=EPS_CLAIMED,
        event=lambda out: out == 1,
        event_description=EVENT_DESC,
        trials_per_dataset=TRIALS,
        alpha=ALPHA,
        seed=SEED,
    )
    print(bad.render())

    print("\nReminder: 'consistent_not_proof' is statistical evidence only;")
    print("no finite experiment can prove differential privacy.")


if __name__ == "__main__":
    main()
