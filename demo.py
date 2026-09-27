"""Runnable demo of the exact alias sampler (Python 3.11, stdlib only).

Output convention:
  [EXACT]     conclusions proved by exact rational arithmetic / enumeration
  [EMPIRICAL] estimates from finite random runs (seed and draws reported)
"""

import random
from fractions import Fraction

from alias_sampler import (
    AliasTable,
    AliasTablePublisher,
    empirical_frequencies,
    enumerate_exact_distribution,
)


def show(title):
    print(f"\n=== {title} ===")


def main():
    show("1. Exact construction with tiny / zero weights")
    weights = [Fraction(1, 10**12), 2, 0, 3, Fraction(1, 7)]
    table = AliasTable(weights)
    print("weights:           ", [str(w) for w in table.weights])
    print("normalized (exact):", [str(p) for p in table.normalized_weights()])
    print("table probs (exact):", [str(p) for p in table.probabilities()])
    ok = table.probabilities() == table.normalized_weights()
    print(f"[EXACT] table reconstruction == normalized weights: {ok}")
    print(f"[EXACT] probabilities sum to exactly 1: "
          f"{sum(table.probabilities()) == 1}")

    show("2. Exact enumeration proof on a small weight set")
    small = AliasTable([1, 2, 3])
    enumerated = enumerate_exact_distribution(small)
    print("weights [1, 2, 3], enumerated outcomes:",
      sum(p.denominator for p in small.prob), "equiprobable (column, draw) pairs")
    print("[EXACT] enumerated distribution:", [str(p) for p in enumerated])
    print(f"[EXACT] equals normalized weights: "
          f"{enumerated == small.normalized_weights()}")

    show("3. Empirical sampling (caller-injected RNG)")
    seed = 20260927
    draws = 100_000
    rng = random.Random(seed)
    freqs, counts = empirical_frequencies(small, rng, draws)
    print(f"seed={seed}, draws={draws}")
    print("[EMPIRICAL] frequencies:", [round(f, 5) for f in freqs])
    print("[EXACT]     probabilities:", [float(p) for p in small.normalized_weights()])

    show("4. Serialization round-trip + replay")
    blob = table.dumps()
    restored = AliasTable.loads(blob)
    print(f"serialized {len(blob)} bytes of JSON")
    print(f"[EXACT] restored table identical: "
          f"{restored.prob == table.prob and restored.alias == table.alias}")
    print(f"[EXACT] restored distribution unbiased: "
          f"{restored.probabilities() == table.normalized_weights()}")

    publisher = AliasTablePublisher(weights)
    session = publisher.open_session(seed=42)
    produced = session.sample(10)
    print(f"session state: {session.state_dict()}")
    print(f"replay matches produced sequence: {session.replay() == produced}")

    show("5. Dual-version publishing (batch weight update)")
    old_session = publisher.open_session(seed=7)
    publisher.publish_update([100, 1, 0, 0, 0])
    new_session = publisher.open_session(seed=7)
    print(f"[EXACT] old session v{old_session.version} distribution:",
          [str(p) for p in old_session.table.probabilities()])
    print(f"[EXACT] new session v{new_session.version} distribution:",
          [str(p) for p in new_session.table.probabilities()])

    show("6. Edge cases")
    single = AliasTable([0, 0, 5, 0])
    rng = random.Random(1)
    picks = {single.sample(rng) for _ in range(1000)}
    print(f"[EXACT] single positive weight -> always item 2: {picks == {2}}")
    for bad in ([1, -1], [1.0, float("inf")], [float("nan")]):
        try:
            AliasTable(bad)
            print(f"ERROR: {bad} accepted")
        except Exception as exc:
            print(f"rejected {bad}: {exc}")


if __name__ == "__main__":
    main()
