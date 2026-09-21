"""Walk through the four canonical explanations of a 250 -> 3 reading.

Run: python3.11 examples/scenarios.py
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from counter_analysis import CounterStore, Sample, SourceConfig

CFG = SourceConfig(bit_width=8, max_interval=60.0, config_version="v1")


def show(title, store):
    print(f"\n== {title} ==")
    for d in store.diffs("dev"):
        print(f"  {str(d.left_id):>5} -> {d.right_id:<5} "
              f"{d.status.value:<10} delta={d.delta}  ({d.reason}) [{d.config_version}]")
    t = store.totals("dev")
    print(f"  determined_total={t.determined_total} complete={t.complete} "
          f"uncertain={list(t.uncertain_intervals)}")


def fresh():
    store = CounterStore()
    store.configure("dev", CFG)
    return store


# 1. 250 -> 3 within the decidable interval: exactly one wrap, delta = 9.
store = fresh()
store.insert(Sample("a", "dev", 0.0, 1, 250))
store.insert(Sample("b", "dev", 10.0, 2, 3))
show("250 -> 3, gap 10s: determined wrap (+9)", store)

# 2. Same numbers, but the second sample carries an explicit reset flag.
store = fresh()
store.insert(Sample("a", "dev", 0.0, 1, 250))
store.insert(Sample("b", "dev", 10.0, 2, 3, reset=True))
show("250 -> 3 with reset flag: restart at 0 (+3)", store)

# 3. Same numbers after a 150s gap: unknowable, reported as uncertain.
store = fresh()
store.insert(Sample("a", "dev", 0.0, 1, 250))
store.insert(Sample("d", "dev", 150.0, 4, 3))
show("250 -> 3, gap 150s: uncertain", store)

# 4. Backfilled middle samples resolve the gap piece by piece.
rev = store.insert(Sample("b", "dev", 30.0, 2, 10))
print("\n-- backfill b@30s: invalidated",
      [d.key for d in rev.invalidated], "emitted", [d.key for d in rev.emitted])
rev = store.insert(Sample("c", "dev", 90.0, 3, 200))
print("-- backfill c@90s: invalidated",
      [d.key for d in rev.invalidated], "emitted", [d.key for d in rev.emitted])
show("after backfill: fully resolved", store)
