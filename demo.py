"""Runnable demo of the exact alias table (Python 3.11, stdlib only).

Run:  python3.11 demo.py
"""

from collections import Counter
from fractions import Fraction

from alias_table import AliasTable, AliasTablePublisher, SeededRandomSource


def main() -> None:
    print("=== 1. Exact construction with rational and tiny weights ===")
    weights = [Fraction(1, 10**12), 3, 0, Fraction(2, 7), 1]
    table = AliasTable(weights)
    print("weights          :", [str(w) for w in table.weights])
    print("normalized       :", [str(p) for p in table.normalized_weights])
    print("table probabilities (EXACT, reconstructed):",
          [str(p) for p in table.probabilities()])
    assert table.probabilities() == table.normalized_weights
    print("reconstruction == normalized weights     : True (exact)")

    print("\n=== 2. Seeded sampling, serialization, replay ===")
    source = SeededRandomSource(seed=20260927)
    sample = table.sample(source, 10)
    print("sample (seed=20260927):", sample)
    print("source bookkeeping    : seed=%r, integers drawn=%d"
          % (source.seed, source.draws))

    payload = table.dumps()
    restored = AliasTable.loads(payload)
    replay = restored.sample(SeededRandomSource(seed=20260927), 10)
    print("serialized bytes      :", len(payload))
    print("replay after restore  :", replay)
    print("replay identical      :", replay == sample, "(exact)")

    print("\n=== 3. Batch weight update with dual-version publishing ===")
    publisher = AliasTablePublisher([1, 1, 1])
    old_session = publisher.open_session()
    print("published version     :", publisher.version)
    publisher.update_weights([10, 1, 1])  # batch update, atomic swap
    new_session = publisher.open_session()
    print("after update version  :", publisher.version)
    print("old session (v%d) probs:" % old_session.version,
          [str(p) for p in old_session.table.probabilities()])
    print("new session (v%d) probs:" % new_session.version,
          [str(p) for p in new_session.table.probabilities()])
    old_draws = old_session.sample(SeededRandomSource(seed=7), 8)
    new_draws = new_session.sample(SeededRandomSource(seed=7), 8)
    print("old session draws     :", old_draws,
          "(samples_drawn=%d)" % old_session.samples_drawn)
    print("new session draws     :", new_draws,
          "(samples_drawn=%d)" % new_session.samples_drawn)

    print("\n=== 4. EMPIRICAL frequency check (not an exact proof) ===")
    trials = 100_000
    empirical_table = AliasTable([1, 2, 3, 4])
    counts = Counter(empirical_table.sample(SeededRandomSource(seed=1), trials))
    for i, p in enumerate(empirical_table.normalized_weights):
        freq = counts.get(i, 0) / trials
        print("item %d: exact=%s empirical=%.4f" % (i, p, freq))


if __name__ == "__main__":
    main()
