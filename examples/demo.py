"""End-to-end demo: composite-index compilation, MVCC paging and costs."""
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from composite_index import (
    NULL, ColumnSpec, Cmp, In, Or, Database, full_scan_interpret,
)


def main():
    db = Database(["a", "b"])
    db.create_index("idx_ab", [ColumnSpec("a"), ColumnSpec("b", desc=True)])
    db.create_index("idx_b", [ColumnSpec("b", nulls_first=True)])

    import random
    rng = random.Random(42)
    for _ in range(600):
        a = rng.choice([NULL] + list(range(30)))
        b = rng.choice([NULL] + list(range(30)))
        db.insert({"a": a, "b": b})

    pred = Or((Cmp("a", "eq", 1), In("b", (0, 1))))
    page = db.execute(pred, page_size=4)
    print("chosen plan:", page.plan["kind"])
    print("estimate:", page.plan["estimate"])
    for cand in page.plan["candidates"]:
        print("  candidate:", cand["kind"], "cost =", cand["estimate"]["total"])
    print(json.dumps(page.plan["arms"], indent=2))

    seen = list(page.rows)
    while page.next_token:
        page = db.fetch(page.next_token)
        seen.extend(page.rows)
    print("executed counts:", page.executed)
    print("result rids:", [rid for rid, _ in seen])
    print("interpreter rids:", [rid for rid, _ in
          full_scan_interpret(db, pred, db.mvcc.version)])


if __name__ == "__main__":
    main()
