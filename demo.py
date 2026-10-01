"""Run samples for the exact weighted sampler (Python 3.11, stdlib only).

Labels used in the output:
  [exact]     exact rational conclusions, guaranteed by construction
  [empirical] empirical estimates from finite runs (illustration only,
              NOT part of the unbiasedness argument)
"""

from fractions import Fraction

from random_sources import SeededRandomSource
from weighted_sampler import WeightedSampler, ZeroTotalWeightError


def main():
    print("== 1. Setup: exact rational weights ==")
    source = SeededRandomSource(seed=20261001)
    weights = [Fraction(1, 2), Fraction(1, 3), Fraction(1, 6), Fraction(0)]
    s = WeightedSampler(weights, source=source)
    print("weights:", [str(w) for w in s.weights], " total:", s.total())
    print("intervals:", [(str(a), str(b)) for a, b in s.intervals()])
    print("[exact] P(i) =", [str(w / s.total()) for w in s.weights],
          " (index 3 has weight 0 and can never be drawn)")

    print("\n== 2. Draws with replacement (fixed seed, reproducible) ==")
    draws = [s.draw() for _ in range(6)]
    print("draws:", draws)
    print("last draw info (manual recomputation):", s.last_draw_info)
    print(f"source: seed={source.seed} values_returned={source.values_returned}"
          f" bits_consumed={source.bits_consumed}")

    print("\n== 3. Batch without replacement (transaction + commit) ==")
    tx = s.begin()
    batch = tx.sample(2)
    tx.commit()
    print("batch (ordered, no duplicates):", batch)
    print("weights after commit:", [str(w) for w in s.weights],
          " total:", s.total(), " version:", s.version)
    print("[exact] P(ordered batch) = prod w_i/(remaining total) per step")

    print("\n== 4. Rollback restores weights AND random state ==")
    before_weights = s.weights
    before_values = source.values_returned
    tx = s.begin()
    drawn = tx.draw()
    tx.rollback()
    print("drew then rolled back:", drawn)
    print("weights restored:", s.weights == before_weights,
          " random values unconsumed:", source.values_returned == before_values)

    print("\n== 5. Snapshot versions ==")
    snap = s.snapshot()
    s.set_weight(0, Fraction(5))
    print(f"live:    total={s.total()} version={s.version}")
    print(f"snapshot: total={snap.total()} version={snap.version} (frozen)")

    print("\n== 6. Zero total weight fails explicitly ==")
    empty = WeightedSampler([0, 0], source=SeededRandomSource(1))
    try:
        empty.draw()
    except ZeroTotalWeightError as e:
        print("ZeroTotalWeightError:", e)

    print("\n== 7. Empirical check (illustration only, NOT a proof) ==")
    src = SeededRandomSource(7)
    t = WeightedSampler([Fraction(1, 2), Fraction(1, 3), Fraction(1, 6)],
                        source=src)
    n = 6000
    counts = [0, 0, 0]
    for _ in range(n):
        counts[t.draw()] += 1
    print("[empirical] frequencies:", [round(c / n, 4) for c in counts])
    print("[exact]     probabilities:", [str(Fraction(1, 2)), str(Fraction(1, 3)),
                                          str(Fraction(1, 6))])


if __name__ == "__main__":
    main()
