"""Acceptance: exhaustive predicate enumeration over a small domain,
cross-checked against an independent full-scan interpreter, while updates,
deletes and key migrations are committed between page fetches and every page
boundary goes through a token serialize/resume round-trip."""
import unittest
from dataclasses import replace

from minidb import (And, Cmp, ColSpec, CostModel, Cursor, Database, In,
                    IsNull, Not, Or, Param, compile_query, interpret)
from minidb.intervals import touching

DOMAIN_A = [None, 0, 1, 2]
DOMAIN_B = [None, "p", "q"]
DOMAIN_C = [None, 5, 7]

PARAMS = {"pa": 1, "pb": "p"}


def build_db():
    db = Database(3)
    rows = [(a, b, c) for a in DOMAIN_A for b in DOMAIN_B for c in DOMAIN_C]
    # duplicate physical rows with identical values
    rows += [(1, "p", 5), (None, None, None), (2, "q", 7)]
    for r in rows:
        db.insert(r)
    db.create_index("idx_ab", (ColSpec(0, desc=False, nulls_first=True),
                               ColSpec(1, desc=True, nulls_first=False)))
    db.create_index("idx_b", (ColSpec(1, desc=False, nulls_first=False),))
    db.create_index("idx_c", (ColSpec(2, desc=True, nulls_first=True),))
    return db


def atoms():
    out = []
    for v in (0, 1, 2):
        for op in ("lt", "le", "eq", "ge", "gt", "ne"):
            out.append(Cmp(0, op, v))
    out.append(IsNull(0))
    out.append(In(0, (0, 1)))
    out.append(In(0, (1, 2)))
    out.append(In(0, (None, 2)))
    for v in ("p", "q"):
        out.append(Cmp(1, "eq", v))
        out.append(Cmp(1, "ne", v))
    out.append(IsNull(1))
    out.append(In(1, ("p", "q")))
    out.append(Cmp(2, "eq", 5))
    out.append(Cmp(2, "gt", 5))
    out.append(Cmp(2, "ne", 7))
    out.append(IsNull(2))
    out.append(In(2, (5, 7)))
    out.append(Cmp(0, "eq", Param("pa")))   # parameterised atoms
    out.append(Cmp(1, "eq", Param("pb")))
    return out


def predicates():
    A = atoms()
    P = list(A)
    P += [Not(a) for a in A]
    sel = A[:12]
    for i in range(len(sel)):
        for j in range(i + 1, len(sel)):
            P.append(And(sel[i], sel[j]))
            P.append(Or(sel[i], sel[j]))
    for i in range(0, len(sel), 3):
        P.append(And(Not(sel[i]), sel[(i + 1) % len(sel)]))
        P.append(Or(Not(sel[i]), sel[(i + 2) % len(sel)]))
        P.append(Not(Or(sel[i], sel[(i + 1) % len(sel)])))
    # hand-picked structural cases
    P.append(And(Cmp(0, "eq", 1), Cmp(1, "eq", "p")))        # composite prefix
    P.append(And(Cmp(0, "eq", 1), Cmp(1, "ge", "p")))        # prefix + range
    P.append(Or(Cmp(0, "eq", 1), Cmp(1, "eq", "p")))         # cross-index union
    P.append(Or(In(0, (0, 1)), In(0, (1, 2))))               # overlapping INs
    P.append(Or(Cmp(0, "eq", 0), Cmp(0, "eq", 2), Cmp(2, "eq", 5)))
    P.append(And(Or(Cmp(0, "ge", 1), IsNull(0)), Not(Cmp(1, "eq", "p"))))
    P.append(Not(And(IsNull(0), Or(Cmp(1, "eq", "p"), Cmp(2, "gt", 5)))))
    P.append(Or(And(Cmp(0, "eq", 0), Cmp(1, "eq", "p")),
                And(Cmp(0, "eq", 1), Cmp(1, "eq", "q"))))
    return P


def mutate(db, seed, step):
    """One committed mutation: insert / delete / key-migration update."""
    k = (seed + step) % 3
    if k == 0:
        a = (seed + step) % 4
        db.insert((None if a == 3 else a, "p" if step % 2 else "q",
                   (5 + step) % 9 or None))
        return
    rids = db.visible_rowids(db.version)
    if not rids:
        return
    if k == 1:
        db.delete(rids[(seed * 7 + step * 3) % len(rids)])
    else:
        rid = rids[(seed * 5 + step) % len(rids)]
        vals = list(db.get(rid, db.version))
        # migrate the indexed key columns
        vals[0] = None if vals[0] == 2 else (0 if vals[0] is None else vals[0] + 1)
        vals[1] = "q" if vals[1] == "p" else "p"
        db.update(rid, tuple(vals))


def run_paginated(db, plan, params, sid, page_size, seed):
    """Fetch all pages; between pages commit a mutation and resume from a
    token (exercising the token path on every single page boundary)."""
    cur = Cursor(db, plan, params, snapshot_id=sid, page_size=page_size)
    out = []
    step = 0
    while True:
        page = cur.fetch_page()
        out.extend(page)
        if len(page) < page_size:
            break
        mutate(db, seed, step)
        step += 1
        cur = Cursor.resume(db, cur.token(), plan, params, page_size=page_size)
    return out, cur.stats


def check_intervals_disjoint(testcase, plan):
    plans = plan.children if plan.kind == "union" else [plan]
    for p in plans:
        if p.kind != "range":
            continue
        ivs = p.intervals
        testcase.assertEqual(ivs, sorted(ivs, key=lambda i: (i.lo, not i.lo_inc)))
        for x, y in zip(ivs, ivs[1:]):
            testcase.assertFalse(touching(x, y),
                                 f"overlapping intervals in {p.desc()}")


class TestAcceptance(unittest.TestCase):
    def test_exhaustive_vs_interpreter(self):
        preds = predicates()
        n_runs = 0
        for pi, pred in enumerate(preds):
            for budget in (2, 64):
                cm = CostModel(page_cost=10.0, fetch_cost=4.0, compare_cost=1.0,
                               page_size=8, interval_budget=budget)
                db = build_db()
                sid = db.begin_snapshot()
                ver = db.snapshot_version(sid)
                plan, report = compile_query(db, ver, pred, PARAMS, cm)
                check_intervals_disjoint(self, plan)

                got, stats = run_paginated(db, plan, PARAMS, sid,
                                           page_size=3, seed=pi)
                want = interpret(db, ver, pred, PARAMS)

                self.assertEqual(
                    sorted(got), sorted(want),
                    f"predicate #{pi} budget={budget} plan={report['chosen']}\n"
                    f"pred={pred!r}")
                rids = [r for r, _ in got]
                self.assertEqual(len(rids), len(set(rids)),
                                 "same rowid emitted twice")
                # estimate/execution accounting is always reported
                for key in ("pages", "fetches", "compares", "cost"):
                    self.assertIn(key, plan.est.as_dict())
                for key in ("index_entries", "row_fetches", "compares", "pages"):
                    self.assertIn(key, stats)
                # chosen plan is the cheapest candidate -> reproducible choice
                costs = [c["cost"] for c in report["candidates"]]
                self.assertEqual(plan.est.cost, min(costs))
                db.release_snapshot(sid)
                n_runs += 1

                # forced full scan (no index allowed) must agree too
                plan, report = compile_query(db, ver, pred, PARAMS, cm,
                                             index_names=[])
                self.assertEqual(plan.kind, "full")
                sid = db.begin_snapshot()
                got, _ = run_paginated(db, plan, PARAMS, sid,
                                       page_size=3, seed=pi)
                want2 = interpret(db, db.snapshot_version(sid), pred, PARAMS)
                self.assertEqual(sorted(got), sorted(want2),
                                 f"full scan mismatch for {pred!r}")
                db.release_snapshot(sid)
                n_runs += 1
        print(f"\nacceptance: {n_runs} plan executions "
              f"({len(preds)} predicates x (2 budgets + full scan)) "
              f"all match interpreter")

    def test_union_plans_on_larger_table(self):
        db = Database(3)
        for i in range(300):
            db.insert((i % 10, "p" if i % 7 == 0 else ("q" if i % 5 else None),
                       None if i % 11 == 0 else i % 9))
        db.insert((3, "p", 4))
        db.insert((3, "p", 4))  # identical values, distinct physical rows
        db.create_index("idx_ab", (ColSpec(0, desc=False, nulls_first=True),
                                   ColSpec(1, desc=True, nulls_first=False)))
        db.create_index("idx_b", (ColSpec(1, desc=False, nulls_first=False),))
        db.create_index("idx_c", (ColSpec(2, desc=True, nulls_first=True),))
        cm = CostModel(page_size=16, interval_budget=8)
        union_preds = [
            Or(Cmp(0, "eq", 3), Cmp(1, "eq", "p")),
            Or(Cmp(0, "eq", 1), Cmp(2, "eq", 4)),
            Or(Cmp(1, "eq", "q"), Cmp(2, "ge", 7), Cmp(0, "eq", 0)),
            Or(Cmp(0, "eq", 5), IsNull(2)),
        ]
        seen_union = 0
        for pi, pred in enumerate(union_preds):
            sid = db.begin_snapshot()
            ver = db.snapshot_version(sid)
            plan, report = compile_query(db, ver, pred, PARAMS, cm)
            self.assertEqual(plan.kind, "union", report)
            seen_union += 1
            check_intervals_disjoint(self, plan)
            got, stats = run_paginated(db, plan, PARAMS, sid,
                                       page_size=5, seed=pi + 1000)
            want = interpret(db, ver, pred, PARAMS)
            self.assertEqual(sorted(got), sorted(want),
                             f"union mismatch for {pred!r}")
            rids = [r for r, _ in got]
            self.assertEqual(len(rids), len(set(rids)))
            db.release_snapshot(sid)
        self.assertGreater(seen_union, 0)

    def test_estimates_vs_executed_on_range_plans(self):
        db = build_db()
        sid = db.begin_snapshot()
        ver = db.snapshot_version(sid)
        cm = CostModel(page_size=8)
        for pred in (Cmp(0, "eq", 1), And(Cmp(0, "ge", 1), Cmp(1, "eq", "p")),
                     IsNull(0), Cmp(0, "lt", 2)):
            plan, _ = compile_query(db, ver, pred, PARAMS, cm)
            if plan.kind != "range":
                continue
            got, stats = run_paginated(db, plan, PARAMS, sid,
                                       page_size=4, seed=0)
            self.assertEqual(stats["index_entries"], plan.est.fetches)
            self.assertLessEqual(stats["compares"], plan.est.compares)
            self.assertEqual(sorted(got),
                             sorted(interpret(db, ver, pred, PARAMS)))
        db.release_snapshot(sid)


if __name__ == "__main__":
    unittest.main()
