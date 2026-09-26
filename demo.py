"""Demo: plan explanation, execution counts, spill + cleanup verification.

Run with: python3.11 demo.py
"""

import os
import random
import tempfile

from minirel import (
    And, Col, CorrCond, Eq, ExistsPred, InPred, Lit, Query, Subquery,
    run_query,
)


def main():
    rng = random.Random(42)

    # Skewed synthetic multisets: inner key 1 dominates, outer keys are
    # spread out, nulls present on both sides.
    outer = []
    for _ in range(300):
        k = rng.choices([None, 0, 1, 2, 3, 4, 5], weights=[1, 2, 4, 2, 3, 3, 3])[0]
        v = rng.choices([None, 0, 1], weights=[1, 3, 3])[0]
        outer.append((k, v))
    inner = []
    for _ in range(500):
        k = rng.choices([None, 0, 1, 2], weights=[1, 2, 15, 1])[0]
        v = rng.choices([None, 0, 1], weights=[2, 3, 3])[0]
        inner.append((k, v))
    tables = {"o": outer, "i": inner}

    # SELECT c0, c1 FROM o
    # WHERE (c0, c1) NOT IN (SELECT c0, c1 FROM i WHERE i.c0 <=> o.c0)
    #   AND EXISTS (SELECT 1 FROM i WHERE i.c1 = 1)
    notin = InPred(
        (Col(0), Col(1)),
        Subquery("i", select=(Col(0), Col(1)),
                 corr=(CorrCond(Col(0), Col(0), True),)),
        negated=True,
    )
    exists = ExistsPred(Subquery("i", filter=Eq(Col(1), Lit(1))))
    query = Query("o", select=(Col(0), Col(1)), where=And((notin, exists)))

    pages = [outer[i:i + 17] for i in range(0, len(outer), 17)]  # paged input

    with tempfile.TemporaryDirectory() as tmp:
        spill_dir = os.path.join(tmp, "spill")

        interp = run_query(query, tables, pages, mode="interpreter")
        opt = run_query(query, tables, pages, mode="optimized",
                        mem_budget=16, spill_dir=spill_dir)

        print("=== Plan (optimized) ===")
        print(opt.plan)
        print()
        print("=== Results identical:", interp.rows == opt.rows, "===")
        print(f"result rows: {opt.stats.result_rows} "
              f"(outer rows: {opt.stats.outer_rows})")
        print()
        print("=== Execution counts ===")
        print(f"interpreter: inner rows scanned = "
              f"{interp.stats.inner_rows_scanned}")
        print(f"optimized  : inner rows indexed = "
              f"{opt.stats.inner_rows_indexed}, "
              f"index lookups = {opt.stats.index_lookups}, "
              f"partitions spilled = {opt.stats.partitions_spilled}, "
              f"partition loads = {opt.stats.partition_loads}")
        leftover = os.listdir(spill_dir) if os.path.exists(spill_dir) else []
        print()
        print("=== Temp file cleanup ===")
        print(f"spill dir leftover files: {leftover} -> "
              f"{'OK' if not leftover else 'LEAK!'}")


if __name__ == "__main__":
    main()
