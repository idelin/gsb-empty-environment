"""End-to-end demo: build a table, compile a query, paginate with tokens,
mutate between pages, and print the cost report."""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))
from minidb import (And, Cmp, ColSpec, CostModel, Cursor, Database, IsNull,
                    Or, Param, compile_query, interpret)

db = Database(3)
for i in range(200):
    db.insert((i % 10, "p" if i % 7 == 0 else "q", None if i % 11 == 0 else i))
db.create_index("idx_ab", (ColSpec(0, desc=False, nulls_first=True),
                           ColSpec(1, desc=True, nulls_first=False)))
db.create_index("idx_b", (ColSpec(1, desc=False, nulls_first=False),))

cm = CostModel(page_cost=10.0, fetch_cost=4.0, compare_cost=1.0,
               page_size=16, interval_budget=8)
pred = Or(And(Cmp(0, "eq", Param("a")), Cmp(1, "eq", "p")), Cmp(1, "eq", "q"))
params = {"a": 3}

sid = db.begin_snapshot()
ver = db.snapshot_version(sid)
plan, report = compile_query(db, ver, pred, params, cm)

print("candidates:")
for c in report["candidates"]:
    print(f"  {c['desc']:<28} pages={c['pages']:<3} fetches={c['fetches']:<4}"
          f"compares={c['compares']:<4} cost={c['cost']}")
print(f"chosen: {report['chosen']}  kind={plan.kind}")

cur = Cursor(db, plan, params, snapshot_id=sid, page_size=25)
got = []
step = 0
while True:
    page = cur.fetch_page()
    got.extend(page)
    if len(page) < 25:
        break
    step += 1
    db.update(step, (90 + step, "z", step))   # concurrent key migration
    db.delete(150 + step)                     # concurrent delete
    db.insert((step % 10, "p", 1000 + step))  # concurrent insert
    cur = Cursor.resume(db, cur.token(), plan, params, page_size=25)

want = interpret(db, ver, pred, params)
assert sorted(got) == sorted(want), "plan result diverged from interpreter"
print(f"rows={len(got)}  executed={cur.stats}")
print("matches independent interpreter at pinned version", ver)
db.release_snapshot(sid)
