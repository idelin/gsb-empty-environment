"""End-to-end demo of the weighted_quantiles package.

Run from the repository root:  python3 examples/demo.py
"""

import os
import random
import sys
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from weighted_quantiles import (WeightedStore, merge_shards,
                                run_random_transactions, split_shards)


def show(store, q):
    r = store.quantile(q)
    kind = "interval" if r.is_interval else "point   "
    print(f"  q={str(q):>5} -> [{r.low}, {r.high}]  ({kind}, label={r.label})")


def main():
    print("== 1. Exact weighted quantiles (rational weights) ==")
    store = WeightedStore(random.Random(2024))
    store.upsert("a", 10, Fraction(1, 3))
    store.upsert("b", 20, Fraction(1, 2))
    store.upsert("c", 30, Fraction(1, 6))
    store.upsert("d", 20, Fraction(1, 4))   # duplicate value merges into 20
    store.upsert("e", 99, 0)                # zero weight: eliminated
    print("  merged items:", [(v, str(w)) for v, w in store.items()])
    print("  total weight:", store.total_weight())
    for q in (Fraction(0), Fraction(1, 3), Fraction(1, 2), Fraction(3, 4), Fraction(1)):
        show(store, q)
    print("  cdf(20) =", store.cdf(20))

    print("\n== 2. Idempotent update / retraction keeps total weight ==")
    before = store.total_weight()
    store.upsert("a", 10, Fraction(1, 3))   # identical replay: no-op
    print("  replay identical upsert: total unchanged ->",
          store.total_weight() == before)
    store.upsert("a", 10, Fraction(2, 3))   # genuine update
    print("  updated a: total =", store.total_weight())
    store.retract("a")
    print("  retracted a: total =", store.total_weight())

    print("\n== 3. Subject retraction and sliding-window expiry ==")
    win = WeightedStore(random.Random(1), window=10)
    win.upsert("s1", 1, 1, subject="alice", timestamp=0)
    win.upsert("s2", 2, 1, subject="alice", timestamp=5)
    win.upsert("s3", 3, 1, subject="bob", timestamp=9)
    print("  retract_subject('alice') removed", win.retract_subject("alice"))
    win.upsert("s4", 4, 1, subject="carol", timestamp=8)
    print("  advance(20) expired:", win.advance(20))
    print("  remaining:", [(v, str(w)) for v, w in win.items()])

    print("\n== 4. Shard split / merge ==")
    big = WeightedStore(random.Random(7))
    rng = random.Random(11)
    for i in range(500):
        big.upsert(f"o{i}", rng.randint(0, 100),
                   Fraction(rng.randint(1, 9), rng.randint(1, 7)))
    shards = split_shards(big, 4, seed=5)
    merged = merge_shards(shards)
    print("  shards:", [len(s) for s in shards],
          "| merged == original:",
          list(merged.items()) == list(big.items()))

    print("\n== 5. Random-transaction verification vs naive reference ==")
    big.reset_stats()
    for num in range(1, 100):
        big.quantile(Fraction(num, 100))
    print(f"  99 quantile queries over {len(big)} observations: "
          f"{big.node_visits()} treap node visits (no full re-sort)")
    report = run_random_transactions(seed=20261001, n_ops=3000)
    print(f"  label={report['label']}  seed={report['seed']}  "
          f"samples={report['samples']}  checks={report['checks']}")
    print(f"  ok={report['ok']}  mismatches={len(report['mismatches'])}")
    print(f"  treap node visits={report['treap_node_visits']}  "
          f"reference full sorts={report['reference_full_sorts']}  "
          f"reference rows scanned={report['reference_rows_scanned']}")


if __name__ == "__main__":
    main()
