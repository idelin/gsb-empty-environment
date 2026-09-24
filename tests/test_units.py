import unittest

from minidb import (And, Cmp, ColSpec, CostModel, Cursor, Database, In,
                    IsNull, Not, Or, Param, PlanMismatch, SnapshotReclaimed,
                    TokenError, compile_query, interpret, plan_fingerprint)
from minidb.intervals import normalize, touching


def build_db(rows, with_indexes=True):
    db = Database(3)
    for r in rows:
        db.insert(r)
    if with_indexes:
        db.create_index("idx_ab", (ColSpec(0, desc=False, nulls_first=True),
                                   ColSpec(1, desc=True, nulls_first=False)))
        db.create_index("idx_b", (ColSpec(1, desc=False, nulls_first=False),))
        db.create_index("idx_c", (ColSpec(2, desc=True, nulls_first=True),))
    return db


def run_all(db, plan, params=None, page_size=100, sid=None):
    own = sid is None
    if own:
        sid = db.begin_snapshot()
    cur = Cursor(db, plan, params, snapshot_id=sid, page_size=page_size)
    out = []
    while True:
        page = cur.fetch_page()
        out.extend(page)
        if len(page) < page_size:
            break
    if own:
        db.release_snapshot(sid)
    return out, cur.stats


class TestOrdering(unittest.TestCase):
    def test_asc_desc_null_placement(self):
        db = Database(2)
        data = [(0, "p"), (0, "q"), (0, None), (1, "q"),
                (None, "p"), (2, None), (1, None)]
        for vals in data:
            db.insert(vals)
        db.create_index("idx", (ColSpec(0, desc=False, nulls_first=True),
                                ColSpec(1, desc=True, nulls_first=False)))
        ver = db.version
        got = [db.get(r, ver) for _, r in db.indexes["idx"].view(ver)]
        # a ASC nulls-first; within a: b DESC nulls-last
        self.assertEqual(got, [(None, "p"), (0, "q"), (0, "p"), (0, None),
                               (1, "q"), (1, None), (2, None)])

    def test_index_scan_returns_index_order(self):
        rows = [(i % 3, "p" if i % 2 else "q", i) for i in range(9)]
        db = build_db(rows)
        sid = db.begin_snapshot()
        ver = db.snapshot_version(sid)
        plan, _ = compile_query(db, ver, Cmp(0, "ge", 0), index_names=["idx_ab"])
        self.assertEqual(plan.kind, "range")
        out, _ = run_all(db, plan, sid=sid)
        db.release_snapshot(sid)
        keys = [(v[0], v[1]) for _, v in out]
        # a ascending; b descending within equal a
        self.assertEqual([a for a, _ in keys], sorted(a for a, _ in keys))
        for a in (0, 1, 2):
            bs = [b for a2, b in keys if a2 == a]
            self.assertEqual(bs, sorted(bs, reverse=True))


class TestIntervals(unittest.TestCase):
    def setUp(self):
        self.db = build_db([(a, b, c)
                            for a in (None, 0, 1, 2)
                            for b in (None, "p", "q")
                            for c in (None, 5, 7)])
        self.sid = self.db.begin_snapshot()
        self.ver = self.db.snapshot_version(self.sid)

    def tearDown(self):
        self.db.release_snapshot(self.sid)

    def test_null_endpoint_is_null(self):
        plan, _ = compile_query(self.db, self.ver, IsNull(0))
        self.assertEqual(plan.kind, "range")
        self.assertEqual(len(plan.intervals), 1)
        iv = plan.intervals[0]
        self.assertEqual(iv.lo[0], iv.hi[0])  # point on the NULL component
        out, _ = run_all(self.db, plan, sid=self.sid)
        self.assertTrue(all(v[0] is None for _, v in out))
        self.assertEqual(len(out), 9)

    def test_overlapping_ranges_merge(self):
        plan, _ = compile_query(self.db, self.ver,
                                Or(Cmp(0, "ge", 1), Cmp(0, "le", 2)),
                                cm=CostModel(interval_budget=16))
        ivs = plan.intervals
        for x, y in zip(ivs, ivs[1:]):
            self.assertFalse(touching(x, y), "intervals must be disjoint")
        # ge 1 and le 2 overlap heavily: everything from 1..2 plus NULL points
        out, _ = run_all(self.db, plan, sid=self.sid)
        want = interpret(self.db, self.ver, Or(Cmp(0, "ge", 1), Cmp(0, "le", 2)))
        self.assertEqual(sorted(out), sorted(want))

    def test_overlapping_in_sets(self):
        pred = Or(In(0, (0, 1)), In(0, (1, 2)))
        plan, _ = compile_query(self.db, self.ver, pred)
        out, _ = run_all(self.db, plan, sid=self.sid)
        want = interpret(self.db, self.ver, pred)
        self.assertEqual(sorted(out), sorted(want))
        rids = [r for r, _ in out]
        self.assertEqual(len(rids), len(set(rids)), "rowid read more than once")

    def test_suffix_only_constraint_is_not_a_narrow_prefix(self):
        plan, _ = compile_query(self.db, self.ver, Cmp(1, "eq", "p"),
                                index_names=["idx_ab"])
        self.assertEqual(plan.kind, "range")
        self.assertEqual(len(plan.intervals), 1)
        iv = plan.intervals[0]
        # wide interval: first component spans the whole domain of column a
        self.assertEqual(iv.lo[0], (0, None))
        self.assertEqual(iv.hi[0], (4, None))
        out, _ = run_all(self.db, plan, sid=self.sid)
        want = interpret(self.db, self.ver, Cmp(1, "eq", "p"))
        self.assertEqual(sorted(out), sorted(want))

    def test_interval_budget_degrades_to_superset(self):
        pred = Or(*(Cmp(0, "eq", v) for v in (0, 1, 2)),
                  Cmp(0, "eq", 0))  # several disjoint points + null points
        cm = CostModel(interval_budget=1)
        plan, _ = compile_query(self.db, self.ver, pred, cm=cm,
                                index_names=["idx_ab"])
        self.assertEqual(plan.kind, "range")
        self.assertTrue(plan.degraded)
        self.assertEqual(len(plan.intervals), 1)
        out, _ = run_all(self.db, plan, sid=self.sid)
        want = interpret(self.db, self.ver, pred)
        self.assertEqual(sorted(out), sorted(want), "degraded plan lost rows")


class TestNotUnknown(unittest.TestCase):
    def test_not_unknown(self):
        rows = [(0, "p", 1), (1, "q", 2), (2, None, 3), (None, "p", 4)]
        db = build_db(rows)
        sid = db.begin_snapshot()
        ver = db.snapshot_version(sid)
        pred = Not(Cmp(1, "eq", "p"))
        plan, _ = compile_query(db, ver, pred)
        out, _ = run_all(db, plan, sid=sid)
        db.release_snapshot(sid)
        # b=None -> inner UNKNOWN -> NOT UNKNOWN -> excluded
        self.assertEqual(sorted(v for _, v in out), [(1, "q", 2)])


class TestUnion(unittest.TestCase):
    def test_union_dedup_and_duplicate_rows(self):
        # enough rows that the union of two narrow scans beats a wide scan
        rows = []
        for i in range(120):
            rows.append((i % 10, "x", i))
        rows.append((1, "p", 500))          # hit by both branches
        rows.append((1, "p", 500))          # same values, distinct physical row
        rows.append((2, "p", 501))
        db = build_db(rows)
        sid = db.begin_snapshot()
        ver = db.snapshot_version(sid)
        pred = Or(Cmp(0, "eq", 1), Cmp(1, "eq", "p"))
        plan, report = compile_query(db, ver, pred)
        self.assertEqual(plan.kind, "union", report)
        out, stats = run_all(db, plan, sid=sid)
        want = interpret(db, ver, pred)
        self.assertEqual(sorted(out), sorted(want))
        rids = [r for r, _ in out]
        self.assertEqual(len(rids), len(set(rids)), "same rowid emitted twice")
        # the two identical physical rows are both present
        self.assertEqual(sum(1 for _, v in out if v == (1, "p", 500)), 2)
        db.release_snapshot(sid)


class TestMVCC(unittest.TestCase):
    def test_old_cursor_survives_key_migration(self):
        db = build_db([(0, "p", 1), (1, "q", 2), (2, "p", 3), (3, "q", 4)])
        sid = db.begin_snapshot()
        ver = db.snapshot_version(sid)
        pred = Cmp(0, "ge", 0)
        plan, _ = compile_query(db, ver, pred)
        cur = Cursor(db, plan, snapshot_id=sid, page_size=2)
        page1 = cur.fetch_page()
        self.assertEqual(len(page1), 2)
        # migrate keys and delete a row in newer versions
        db.update(1, (99, "p", 1))
        db.delete(2)
        db.insert((5, "z", 9))
        rest = []
        while True:
            page = cur.fetch_page()
            rest.extend(page)
            if len(page) < 2:
                break
        got = page1 + rest
        want = interpret(db, ver, pred)
        self.assertEqual(sorted(got), sorted(want),
                         "cursor must keep reading the pinned old version")
        # a fresh snapshot sees the new state
        sid2 = db.begin_snapshot()
        ver2 = db.snapshot_version(sid2)
        self.assertNotEqual(sorted(interpret(db, ver2, pred)), sorted(got))
        db.release_snapshot(sid)
        db.release_snapshot(sid2)


class TestTokens(unittest.TestCase):
    def setUp(self):
        self.db = build_db([(i % 4, "p" if i % 2 else "q", i) for i in range(20)])
        self.sid = self.db.begin_snapshot()
        self.ver = self.db.snapshot_version(self.sid)

    def tearDown(self):
        self.db.release_snapshot(self.sid)

    def test_resume_matches_continuous(self):
        plan, _ = compile_query(self.db, self.ver, Cmp(0, "ge", 1))
        whole, _ = run_all(self.db, plan, sid=self.sid, page_size=100)
        cur = Cursor(self.db, plan, snapshot_id=self.sid, page_size=3)
        got = []
        while True:
            page = cur.fetch_page()
            got.extend(page)
            if len(page) < 3:
                break
            cur = Cursor.resume(self.db, cur.token(), plan, page_size=3)
        self.assertEqual(got, whole)

    def test_token_rejects_wrong_params(self):
        pred = Cmp(0, "eq", Param("x"))
        params = {"x": 2}
        plan, _ = compile_query(self.db, self.ver, pred, params)
        cur = Cursor(self.db, plan, params, snapshot_id=self.sid, page_size=2)
        cur.fetch_page()
        tok = cur.token()
        with self.assertRaises(PlanMismatch):
            Cursor.resume(self.db, tok, plan, {"x": 3})
        with self.assertRaises(PlanMismatch):
            plan2, _ = compile_query(self.db, self.ver, Cmp(0, "eq", 2))
            Cursor.resume(self.db, tok, plan2)
        # correct plan+params resumes fine
        Cursor.resume(self.db, tok, plan, params)

    def test_token_rejects_reclaimed_snapshot(self):
        plan, _ = compile_query(self.db, self.ver, Cmp(0, "ge", 0))
        cur = Cursor(self.db, plan, snapshot_id=self.sid, page_size=2)
        cur.fetch_page()
        tok = cur.token()
        self.db.release_snapshot(self.sid)
        with self.assertRaises(SnapshotReclaimed):
            Cursor.resume(self.db, tok, plan)
        self.sid = self.db.begin_snapshot()  # so tearDown doesn't fail

    def test_malformed_token(self):
        plan, _ = compile_query(self.db, self.ver, Cmp(0, "ge", 0))
        with self.assertRaises(TokenError):
            Cursor.resume(self.db, "not-json", plan)


class TestCost(unittest.TestCase):
    def test_reproducible_and_min_cost(self):
        db = build_db([(i % 4, "p" if i % 2 else "q", i % 7) for i in range(40)])
        sid = db.begin_snapshot()
        ver = db.snapshot_version(sid)
        pred = And(Cmp(0, "ge", 1), Or(Cmp(1, "eq", "p"), IsNull(1)))
        plan1, rep1 = compile_query(db, ver, pred)
        plan2, rep2 = compile_query(db, ver, pred)
        self.assertEqual(plan_fingerprint(plan1), plan_fingerprint(plan2))
        costs = [c["cost"] for c in rep1["candidates"]]
        self.assertEqual(plan1.est.cost, min(costs))
        self.assertEqual(rep1["chosen"], plan1.desc())
        # executed counters line up with estimates for a plain range scan
        if plan1.kind == "range":
            _, stats = run_all(db, plan1, sid=sid)
            self.assertEqual(stats["index_entries"], plan1.est.fetches)
            self.assertLessEqual(stats["compares"], plan1.est.compares)
        db.release_snapshot(sid)


if __name__ == "__main__":
    unittest.main()
