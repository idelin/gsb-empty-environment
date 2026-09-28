"""End-to-end walkthrough:  python3.11 demo.py

Scenario: three hypotheses about a coin (fair / heads-biased / tails-biased).
The demo shows sequential log-domain updating, a sensor glitch that injects
mutually contradictory evidence, conflict detection, retraction-based
recovery, shard merging, and exact-vs-empirical results.
"""
import random
from fractions import Fraction

from bayes_tree import (
    BayesianUpdater,
    ConflictError,
    ExactRationalModel,
    ObservationShard,
    empirical_posterior,
    merge_shards,
)

HYPS = ("H_fair", "H_head", "H_tail")
PRIOR = {"H_fair": 1 / 3, "H_head": 1 / 3, "H_tail": 1 / 3}
HEAD = {"H_fair": 0.5, "H_head": 0.8, "H_tail": 0.2}
TAIL = {"H_fair": 0.5, "H_head": 0.2, "H_tail": 0.8}


def show(title, updater):
    print(f"--- {title}")
    for record in updater.history():
        marker = " CONFLICT" if record.conflict else ""
        print(f"  step {record.step}: {record.operation:<14} "
              f"logZ={record.log_evidence:.6g} (prior v{record.prior_version}){marker}")
    try:
        posterior = updater.posterior()
        rendered = ", ".join(f"{h}={p:.4f}" for h, p in posterior.items())
        print(f"  posterior: {rendered}")
        print(f"  evidence : P(D)={updater.evidence():.6g}")
    except ConflictError as exc:
        print(f"  posterior: ConflictError -> {exc}")
    print()


def main():
    updater = BayesianUpdater(HYPS, PRIOR)
    updater.add_observation("flip1", HEAD)
    updater.add_observation("flip2", HEAD)
    show("two heads, sequential log-domain updates", updater)

    # late-arriving observation from an older log file: arrival order must not
    # change the answer (contribution tree is order-independent).
    updater.add_observation("flip0_from_archive", TAIL)
    show("late observation from archive appended", updater)

    # the sensor then reports a corrected value for flip1...
    updater.correct_observation("flip1", TAIL)
    show("flip1 corrected HEAD -> TAIL", updater)

    # ...followed by a glitch: evidence ruling out every hypothesis at once.
    updater.add_observation(
        "glitch", {"H_fair": 0.0, "H_head": 0.0, "H_tail": 0.0}
    )
    show("all-zero-likelihood glitch added (no uniform reset)", updater)

    # retraction recovers exactly: the tree recomputes nodes, never divides
    updater.retract_observation("glitch")
    show("glitch retracted -> exact recovery via contribution tree", updater)

    # atomic, versioned prior update
    updater.update_prior(
        {"H_fair": 0.1, "H_head": 0.8, "H_tail": 0.1}, version=2
    )
    show("prior updated to v2 (heads-biased)", updater)

    # shards from two independent collectors share an observation id;
    # merging must count the shared observation exactly once.
    shard_a = ObservationShard(HYPS, name="collector-A")
    shard_a.add_observation("s1", HEAD)
    shard_a.add_observation("shared", TAIL)
    shard_b = ObservationShard(HYPS, name="collector-B")
    shard_b.add_observation("shared", TAIL)
    shard_b.add_observation("s2", HEAD)
    merged = merge_shards(HYPS, PRIOR, [shard_a, shard_b])
    show("two shards merged (shared id counted once)", merged)

    # exact rational reference vs float/log-domain model on a small model
    exact = ExactRationalModel(
        HYPS, {h: Fraction(1, 3) for h in HYPS}
    )
    head_f = {"H_fair": Fraction(1, 2), "H_head": Fraction(4, 5), "H_tail": Fraction(1, 5)}
    tail_f = {"H_fair": Fraction(1, 2), "H_head": Fraction(1, 5), "H_tail": Fraction(4, 5)}
    exact.add_observation("f1", head_f)
    exact.add_observation("f2", tail_f)
    exact_posterior = exact.posterior()
    print("--- exact rational reference (fractions.Fraction)")
    print("  [exact]  " + ", ".join(
        f"{h}={p}" for h, p in exact_posterior.items()
    ))
    print(f"  [exact]  P(D) = {exact.evidence()}")

    seed, samples = 20260928, 20000
    result = empirical_posterior(
        HYPS, PRIOR, {"f1": HEAD, "f2": TAIL},
        rng=random.Random(seed), seed=seed, samples=samples,
    )
    print(f"--- empirical Monte-Carlo estimate (caller-injected rng)")
    print(f"  [empirical seed={result.seed}, n={result.samples}]  "
          + ", ".join(f"{h}={p:.4f}" for h, p in result.estimate.items()))
    print("  [exact float]  "
          + ", ".join(f"{h}={float(p):.4f}" for h, p in exact_posterior.items()))


if __name__ == "__main__":
    main()
