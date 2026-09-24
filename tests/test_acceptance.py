"""Acceptance suite: exhaustive small-domain predicate sweep against an
independent full-scan interpreter, with MVCC mutations between pages."""
import base64
import json
import random
import unittest

from composite_index import (
    NULL, MIN, MAX, ColumnSpec, Cmp, In, IsNull, NotNull, And, Or, Not,
    nnf, Database, TokenError, full_scan_interpret,
)
from composite_index.compiler import (
    compile_node, normalize_intervals, apply_budget, IndexArm, Plan,
)

DOMAIN_A = [NULL, 0, 1, 2]
DOMAIN_B = [NULL, 0, 1]


def build_db():
    db = Database(["a", "b"])
    db.create_index("idx_ab", [ColumnSpec("a"), ColumnSpec("b", desc=True)])
    db.create_index("idx_b", [ColumnSpec("b", nulls_first=True)])
    db.create_index("idx_da", [ColumnSpec("a", desc=True, nulls_first=False),
                               ColumnSpec("b")])
    for a in DOMAIN_A:
        for b in DOMAIN_B:
            db.insert({"a": a, "b": b})
    db.insert({"a": 1, "b": 1})   # duplicate values, distinct physical row
    db.insert({"a": NULL, "b": 1})
    return db


def gen_leaf(rng):
    col = rng.choice(["a", "b"])
    dom = DOMAIN_A if col == "a" else DOMAIN_B
    kind = rng.randrange(6)
    if kind == 0:
        return Cmp(col, rng.choice(["lt", "le", "eq", "ge", "gt", "ne"]),
                   rng.choice(dom))
    if kind == 1:
        return In(col, tuple(rng.sample(dom, rng.randint(1, 3))))
    if kind == 2:
        return IsNull(col)
    if kind == 3:
        return NotNull(col)
    if kind == 4:
        return Cmp(col, "eq", rng.choice(dom))
    return Cmp(col, rng.choice(["lt", "ge"]), rng.choice(dom))


def gen_pred(rng, depth):
    if depth == 0 or rng.random() < 0.45:
        return gen_leaf(rng)
    kind = rng.randrange(3)
    if kind == 0:
        return Not(gen_pred(rng, depth - 1))
    if kind == 1:
        return And(tuple(gen_pred(rng, depth - 1)
                         for _ in range(rng.randint(2, 3))))
    return Or(tuple(gen_pred(rng, depth - 1)
                    for _ in range(rng.randint(2, 3))))


def live_rids(db):
    v = db.mvcc.version
    return [rid for rid in sorted(db.table.rows)
            if db.table.read(rid, v) is not None]


def mutate(db, rng):
    """Random update / delete / key-migration / insert at a new version."""
    live = live_rids(db)
    ops = rng.randint(1, 2)
    for _ in range(ops):
        live = live_rids(db)
        choice = rng.random()
        if not live or choice < 0.35:
            db.insert({"a": rng.choice(DOMAIN_A), "b": rng.choice(DOMAIN_B)})
        elif choice < 0.7 and len(live) > 4:
            db.delete(rng.choice(live))
        else:
            rid = rng.choice(live)
            db.update(rid, {"a": rng.choice(DOMAIN_A),
                            "b": rng.choice(DOMAIN_B)})


def run_paged(db, pred, page_size, budget, mut_rng=None):
    page = db.execute(pred, page_size=page_size, budget=budget)
    rows = list(page.rows)
    plan = page.plan
    while page.next_token:
        if mut_rng is not None:
            mutate(db, mut_rng)
        page = db.fetch(page.next_token)
        rows.extend(page.rows)
    return rows, plan, page.executed


class ExhaustiveTest(unittest.TestCase):
    def test_small_domain_sweep_with_mutations(self):
        db = build_db()
        rng = random.Random(20260924)
        checked = 0
        for i in range(400):
            pred = gen_pred(rng, 3)
            version = db.mvcc.version
            expected = full_scan_interpret(db, pred, version)
            mut_rng = random.Random(5000 + i)
            page_size = rng.randint(1, 4)
            budget = rng.choice([2, 3, 64])
            rows, plan, executed = run_paged(db, pred, page_size, budget,
                                             mut_rng)
            got_ids = [rid for rid, _ in rows]
            want_ids = [rid for rid, _ in expected]
            self.assertEqual(sorted(got_ids), sorted(want_ids),
                             f"pred={pred!r} plan={plan['kind']}")
            self.assertEqual(len(got_ids), len(set(got_ids)),
                             "row id read more than once")
            got_rows = {rid: row for rid, row in rows}
            for rid, row in expected:
                self.assertEqual(got_rows[rid], row)
            self.assertGreaterEqual(executed["entries"], 0)
            checked += 1
        self.assertEqual(checked, 400)


class AllCandidatesTest(unittest.TestCase):
    def test_every_candidate_plan_is_correct(self):
        db = build_db()
        rng = random.Random(31337)
        seen_kinds = set()
        seen_degraded = 0
        for i in range(250):
            pred = gen_pred(rng, 3)
            budget = rng.choice([1, 2, 3, 64])
            version = db.mvcc.version
            want = sorted(r for r, _ in
                          full_scan_interpret(db, pred, version))
            chosen, candidates = db.compile(pred, budget=budget)
            for plan in candidates:
                from composite_index.executor import Executor
                cursor = Executor(db, plan, version, db.costs)
                rows = cursor.next_batch(10_000)
                self.assertTrue(cursor.exhausted)
                got = sorted(r for r, _ in rows)
                self.assertEqual(
                    got, want,
                    f"plan={plan.kind} pred={pred!r} budget={budget}")
                self.assertEqual(len(rows), len({r for r, _ in rows}))
                seen_kinds.add(plan.kind)
                seen_degraded += int(plan.degraded)
        self.assertEqual(seen_kinds, {"index", "scan", "merge"})
        self.assertGreater(seen_degraded, 0)

    def test_paged_continuation_with_mutations_on_each_plan_kind(self):
        db = build_db()
        rng = random.Random(9001)
        covered = set()
        while covered != {"index", "merge", "scan"}:
            pred = gen_pred(rng, 3)
            version = db.mvcc.version
            want = sorted(r for r, _ in
                          full_scan_interpret(db, pred, version))
            chosen, _ = db.compile(pred, budget=rng.choice([1, 2, 64]))
            covered.add(chosen.kind)
            sid = db.begin_snapshot()
            from composite_index.executor import Executor
            cursor = Executor(db, chosen, db._snapshots[sid], db.costs)
            got = []
            mut_rng = random.Random(rng.randrange(10**6))
            while not cursor.exhausted:
                got.extend(r for r, _ in cursor.next_batch(rng.randint(1, 3)))
                mutate(db, mut_rng)
            db.release_snapshot(sid)
            self.assertEqual(sorted(got), want, f"kind={chosen.kind}")


class StructureTest(unittest.TestCase):
    def setUp(self):
        self.db = build_db()

    def intervals_on(self, pred, index_name):
        ix = self.db.indexes[index_name]
        return normalize_intervals(compile_node(nnf(pred), ix), ix.specs)

    def test_desc_prefix_interval(self):
        pred = And((Cmp("a", "eq", 1), Cmp("b", "ge", 0)))
        ivs = self.intervals_on(pred, "idx_ab")
        self.assertEqual(len(ivs), 1)
        iv = ivs[0]
        self.assertIsNone(iv.guard)
        # b DESC, nulls first: value-range b>=0 is (NULL exc) .. 0 inclusive
        self.assertEqual(iv.low, (1, NULL))
        self.assertFalse(iv.low_inc)
        self.assertEqual(iv.high, (1, 0))
        self.assertTrue(iv.high_inc)

    def test_suffix_only_constraint_is_not_a_narrow_prefix(self):
        pred = Cmp("b", "eq", 1)
        ivs = self.intervals_on(pred, "idx_ab")
        self.assertEqual(len(ivs), 1)
        iv = ivs[0]
        self.assertEqual(iv.low, (MIN, MIN))
        self.assertEqual(iv.high, (MAX, MAX))
        self.assertIsNotNone(iv.guard)  # b=1 stays residual

    def test_null_endpoints(self):
        ivs = self.intervals_on(IsNull("a"), "idx_ab")
        self.assertEqual(len(ivs), 1)
        self.assertEqual(ivs[0].low, (NULL, MIN))
        self.assertEqual(ivs[0].high, (NULL, MAX))
        rows, _, _ = run_paged(self.db, IsNull("a"), 3, 8)
        want = full_scan_interpret(self.db, IsNull("a"), self.db.mvcc.version)
        self.assertEqual(sorted(r for r, _ in rows),
                         sorted(r for r, _ in want))
        # comparison with NULL constant is never TRUE: empty, exact
        ivs = self.intervals_on(Cmp("a", "eq", NULL), "idx_ab")
        self.assertEqual(ivs, [])

    def test_overlapping_in_sets(self):
        pred = Or((In("a", (0, 1)), In("a", (1, 2))))
        ivs = self.intervals_on(pred, "idx_ab")
        self.assertEqual(len(ivs), 3)  # merged to disjoint point prefixes
        lows = sorted(iv.low[0] for iv in ivs)
        self.assertEqual(lows, [0, 1, 2])
        rows, plan, executed = run_paged(self.db, pred, 2, 8)
        ids = [r for r, _ in rows]
        self.assertEqual(len(ids), len(set(ids)))
        want = full_scan_interpret(self.db, pred, self.db.mvcc.version)
        self.assertEqual(sorted(ids), sorted(r for r, _ in want))

    def test_not_unknown(self):
        pred = Not(Cmp("a", "gt", 0))
        rows, _, _ = run_paged(self.db, pred, 2, 8)
        for _, row in rows:
            self.assertIsNot(row["a"], NULL)
            self.assertEqual(row["a"], 0)
        want = full_scan_interpret(self.db, pred, self.db.mvcc.version)
        self.assertEqual(sorted(r for r, _ in rows),
                         sorted(r for r, _ in want))
        # NOT IN with NULLs keeps three-valued semantics
        pred2 = Not(In("a", (1, NULL)))
        rows2, _, _ = run_paged(self.db, pred2, 3, 8)
        want2 = full_scan_interpret(self.db, pred2, self.db.mvcc.version)
        self.assertEqual(sorted(r for r, _ in rows2),
                         sorted(r for r, _ in want2))

    def test_duplicate_value_rows_are_two_results(self):
        pred = And((Cmp("a", "eq", 1), Cmp("b", "eq", 1)))
        rows, _, _ = run_paged(self.db, pred, 10, 8)
        self.assertEqual(len(rows), 2)
        self.assertNotEqual(rows[0][0], rows[1][0])

    def test_interval_budget_degrades_to_superset_with_residual(self):
        pred = In("a", (0, 1, 2))
        ix = self.db.indexes["idx_ab"]
        ivs = normalize_intervals(compile_node(nnf(pred), ix), ix.specs)
        self.assertEqual(len(ivs), 3)
        ivs, degraded = apply_budget(ivs, ix.specs, 2)
        self.assertTrue(degraded)
        self.assertEqual(len(ivs), 1)
        self.assertIsNotNone(ivs[0].guard)  # residual compensates the hull
        rows, plan, _ = run_paged(self.db, pred, 2, budget=2)
        want = full_scan_interpret(self.db, pred, self.db.mvcc.version)
        self.assertEqual(sorted(r for r, _ in rows),
                         sorted(r for r, _ in want))
        # nothing silently dropped: all three values present
        self.assertEqual({row["a"] for _, row in rows}, {0, 1, 2})


class EdgeCaseTest(unittest.TestCase):
    def setUp(self):
        self.db = build_db()

    def check(self, pred, budget=8):
        rows, plan, _ = run_paged(self.db, pred, 2, budget)
        want = full_scan_interpret(self.db, pred, self.db.mvcc.version)
        self.assertEqual(sorted(r for r, _ in rows),
                         sorted(r for r, _ in want), f"{pred!r}")
        return rows

    def test_empty_in_list(self):
        self.assertEqual(self.check(In("a", ())), [])

    def test_unknown_column_is_residual(self):
        rows = self.check(Cmp("zzz", "eq", 1))
        self.assertEqual(rows, [])
        rows = self.check(NotNull("a"))
        self.assertTrue(rows)

    def test_double_negation_and_notin(self):
        self.check(Not(Not(Cmp("a", "eq", 1))))
        self.check(Not(In("b", (0, NULL))))
        self.check(And((Not(In("a", (1,))), Cmp("a", "ge", 0))))

    def test_range_on_desc_nulls_last_index(self):
        from composite_index.compiler import compile_node
        ix = self.db.indexes["idx_da"]
        ivs = compile_node(nnf(Cmp("a", "gt", 0)), ix)
        self.assertEqual(len(ivs), 1)
        iv = ivs[0]
        self.assertIsNone(iv.guard)
        v = self.db.mvcc.version
        keys = [k for k, _ in ix.visible(v) if iv.contains(ix.specs, k)]
        self.assertTrue(keys)
        for k in keys:
            self.assertIsNot(k[0], NULL)
            self.assertGreater(k[0], 0)
        self.check(Cmp("a", "gt", 0))
        self.check(And((Cmp("a", "gt", 0), Cmp("b", "le", 1))))

    def test_point_prefix_cartesian(self):
        pred = And((In("a", (0, 2)), In("b", (0, 1))))
        ix = self.db.indexes["idx_ab"]
        ivs = compile_node(nnf(pred), ix)
        self.assertEqual(len(ivs), 4)  # 2 x 2 point prefixes
        self.check(pred)


class MergeAndMvccTest(unittest.TestCase):
    def test_multi_index_merge_reads_row_once(self):
        db = Database(["a", "b"])
        db.create_index("idx_ab", [ColumnSpec("a"), ColumnSpec("b", desc=True)])
        db.create_index("idx_b", [ColumnSpec("b")])
        rng = random.Random(7)
        for _ in range(1000):
            db.insert({"a": rng.randrange(20), "b": rng.randrange(20)})
        pred = Or((Cmp("a", "eq", 1), Cmp("b", "eq", 1)))
        page = db.execute(pred, page_size=16)
        self.assertEqual(page.plan["kind"], "merge")
        self.assertEqual({a["index"] for a in page.plan["arms"]},
                         {"idx_ab", "idx_b"})
        rows = list(page.rows)
        while page.next_token:
            page = db.fetch(page.next_token)
            rows.extend(page.rows)
        ids = [r for r, _ in rows]
        self.assertEqual(len(ids), len(set(ids)), "same row id twice")
        want = full_scan_interpret(db, pred, db.mvcc.version)
        self.assertEqual(sorted(ids), sorted(r for r, _ in want))
        both = [row for _, row in rows if row["a"] == 1 and row["b"] == 1]
        self.assertTrue(both)

    def test_old_cursor_survives_key_migration(self):
        db = build_db()
        pred = Cmp("a", "eq", 1)
        version = db.mvcc.version
        want = full_scan_interpret(db, pred, version)
        page = db.execute(pred, page_size=1)
        first = list(page.rows)
        self.assertEqual(len(first), 1)
        live = [rid for rid, row in want if rid != first[0][0]]
        db.update(live[0], {"a": 9, "b": 0})   # key migration out of the set
        db.delete(live[1])
        db.insert({"a": 1, "b": 1})            # new matching row, newer ver
        rows = list(first)
        while page.next_token:
            page = db.fetch(page.next_token)
            rows.extend(page.rows)
        self.assertEqual(sorted(r for r, _ in rows),
                         sorted(r for r, _ in want))

    def test_token_binding(self):
        db = build_db()
        pred = Cmp("a", "ge", 0)
        page = db.execute(pred, page_size=2)
        token = page.next_token
        self.assertIsNotNone(token)
        # tampered token
        bad = token[:-2] + ("AA" if not token.endswith("AA") else "BB")
        with self.assertRaises(TokenError):
            db.fetch(bad)
        # valid fetch, then replay of the same (now stale) token
        page2 = db.fetch(token)
        self.assertTrue(page2.rows)
        with self.assertRaises(TokenError):
            db.fetch(token)
        # reclaimed snapshot
        page3 = db.execute(pred, page_size=2)
        payload = json.loads(base64.urlsafe_b64decode(
            page3.next_token.rsplit(".", 1)[0].encode()))
        db.release_snapshot(payload["sid"])
        with self.assertRaises(TokenError):
            db.fetch(page3.next_token)
        # token of a finished cursor
        page4 = db.execute(Cmp("a", "eq", 0), page_size=100)
        self.assertIsNone(page4.next_token)


class CostReportTest(unittest.TestCase):
    def test_estimate_and_executed_counts_reproducible(self):
        def run():
            db = build_db()
            pred = Or((Cmp("a", "eq", 1), Cmp("b", "eq", 1)))
            first = db.execute(pred, page_size=4)
            page, rows = first, list(first.rows)
            while page.next_token:
                page = db.fetch(page.next_token)
                rows.extend(page.rows)
            first_executed = page.executed
            return first, first_executed, rows

        page1, exec1, rows1 = run()
        page2, exec2, rows2 = run()
        self.assertEqual(page1.plan["fingerprint"], page2.plan["fingerprint"])
        self.assertEqual(page1.plan["estimate"], page2.plan["estimate"])
        self.assertEqual(exec1, exec2)
        self.assertEqual(rows1, rows2)
        est = page1.plan["estimate"]
        for field in ("pages", "lookups", "compares", "total"):
            self.assertIn(field, est)
        for field in ("pages", "entries", "lookups", "compares"):
            self.assertIn(field, exec1)
        self.assertIn("candidates", page1.plan)
        kinds = {c["kind"] for c in page1.plan["candidates"]}
        self.assertIn("scan", kinds)
        self.assertIn("index", kinds)


if __name__ == "__main__":
    unittest.main()
