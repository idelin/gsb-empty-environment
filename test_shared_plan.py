"""unittest suite for the shared-subplan materialization engine."""
from __future__ import annotations

import os
import tempfile
import unittest

import relalg
from decision import (MATERIALIZE_MEMORY, MATERIALIZE_SPILL, RECOMPUTE,
                      CostModel, decide)
from engine import Executor
from materialize import Catalog
from plan import Filter, Join, Project, Scan, register_predicate


def make_tables():
    departments = relalg.Table(
        "departments", ("dept_id", "dept_name"),
        [(1, "eng"), (2, "sales"), (3, "ops"), (None, "classified")],
    )
    employees = relalg.Table(
        "employees", ("emp_id", "name", "dept_id", "salary"),
        [
            (1, "ada", 1, 90000),
            (2, "alan", 1, 55000),
            (3, "grace", 2, 40000),
            (4, "edsger", None, 70000),
            (5, "barbara", 3, None),
            (3, "grace", 2, 40000),  # duplicate: multiset semantics
        ],
    )
    return employees, departments


register_predicate("p_salary_over_50k", lambda r: relalg.gt(r["salary"], 50000))
register_predicate("p_dept_eng", lambda r: relalg.eq(r["dept_name"], "eng"))
register_predicate("p_keep_all", lambda r: True)


class CountingTable(relalg.Table):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.scan_count = 0

    def scan(self):
        self.scan_count += 1
        return super().scan()


class FailingTable(relalg.Table):
    """Yields one row, then blows up mid-stream."""

    def scan(self):
        def gen():
            yield self.rows[0]
            raise RuntimeError("simulated build failure")
        return gen()


class TestNullSemantics(unittest.TestCase):
    def test_comparisons_with_null_are_unknown(self):
        self.assertIsNone(relalg.eq(None, 1))
        self.assertIsNone(relalg.eq(None, None))
        self.assertIsNone(relalg.gt(None, 5))
        self.assertIsNone(relalg.lt(1, None))
        self.assertIs(relalg.eq(2, 2), True)
        self.assertIs(relalg.gt(3, 5), False)

    def test_three_valued_logic(self):
        self.assertIs(relalg.and_(True, None), None)
        self.assertIs(relalg.and_(False, None), False)
        self.assertIs(relalg.or_(False, None), None)
        self.assertIs(relalg.or_(True, None), True)
        self.assertIsNone(relalg.not_(None))

    def test_filter_keeps_only_true(self):
        rows = [(1,), (None,), (3,)]
        out = list(relalg.filter_rows(rows, lambda r: relalg.gt(r[0], 2)))
        self.assertEqual(out, [(3,)])  # NULL row dropped as UNKNOWN


class TestMultisetSemantics(unittest.TestCase):
    def test_duplicates_preserved_by_filter_and_project(self):
        rows = [(1, "a"), (1, "a"), (2, "b")]
        out = list(relalg.filter_rows(rows, lambda r: relalg.gt(r[0], 0)))
        self.assertEqual(len(out), 3)
        proj = list(relalg.project_rows(rows, (0,)))
        self.assertEqual(proj, [(1,), (1,), (2,)])

    def test_join_multiplies_duplicates(self):
        left = [(1, "x"), (1, "y")]          # 2 rows with key 1
        right = [(1, "p"), (1, "q"), (1, "r")]  # 3 rows with key 1
        out = list(relalg.hash_join_rows(left, right, (0,), (0,)))
        self.assertEqual(len(out), 6)

    def test_join_null_keys_never_match(self):
        left = [(None, "x"), (1, "y")]
        right = [(None, "p"), (1, "q")]
        out = list(relalg.hash_join_rows(left, right, (0,), (0,)))
        self.assertEqual(out, [(1, "y", 1, "q")])


class TestDecisions(unittest.TestCase):
    def test_single_consumer_recomputes(self):
        d = decide(consumers=1, est_rows=100, compute_cost=200.0)
        self.assertEqual(d.strategy, RECOMPUTE)
        self.assertIn("single consumer", d.rationale)

    def test_many_consumers_materialize_in_memory(self):
        d = decide(consumers=5, est_rows=100, compute_cost=200.0)
        self.assertEqual(d.strategy, MATERIALIZE_MEMORY)
        self.assertIn("5 consumers", d.rationale)
        self.assertIn("fits memory quota", d.rationale)

    def test_quota_exceeded_spills(self):
        model = CostModel(mem_quota_bytes=10)  # tiny quota
        d = decide(consumers=3, est_rows=100, compute_cost=500.0, model=model)
        self.assertEqual(d.strategy, MATERIALIZE_SPILL)
        self.assertTrue(d.spilled)
        self.assertIn("spill to disk", d.rationale)

    def test_recompute_when_materialization_too_expensive(self):
        model = CostModel(write_per_row=1000.0, read_per_row=1000.0)
        d = decide(consumers=3, est_rows=100, compute_cost=50.0, model=model)
        self.assertEqual(d.strategy, RECOMPUTE)
        self.assertIn("still cheaper", d.rationale)

    def test_rationale_reports_numbers(self):
        d = decide(consumers=4, est_rows=10, compute_cost=100.0)
        self.assertIn("400.0", d.rationale)   # 4 * 100 recompute
        self.assertIn("100.0", d.rationale)   # compute once


class TestSharing(unittest.TestCase):
    def setUp(self):
        self.employees, self.departments = make_tables()
        self.join = Join(Scan("employees"), Scan("departments"),
                         ("dept_id",), ("dept_id",))

    def test_single_use_not_materialized(self):
        root = Filter("p_dept_eng", self.join)
        ex = Executor([self.employees, self.departments])
        rows = ex.run(root)
        self.assertEqual(len(rows), 2)
        self.assertEqual(ex.catalog.keys(), [])
        for node, d in ex.decision_log:
            self.assertEqual(d.strategy, RECOMPUTE)

    def test_shared_join_materialized_once_filters_not_shared(self):
        employees = CountingTable("employees", self.employees.schema,
                                  self.employees.rows)
        q1 = Project(("name", "salary"),
                     Filter("p_salary_over_50k", self.join))
        q2 = Project(("name",), Filter("p_dept_eng", self.join))
        ex = Executor([employees, self.departments])
        res1, res2 = ex.run_all([q1, q2])

        # join computed exactly once: base table scanned a single time
        self.assertEqual(employees.scan_count, 1)

        # exactly one catalog entry: the *unfiltered* join
        self.assertEqual(ex.catalog.keys(), [self.join.key()])
        view_rows = ex.catalog.get(self.join.key()).read()
        self.assertEqual(len(view_rows), 5)  # NULL dept excluded, dup kept
        self.assertTrue(all(len(r) == 6 for r in view_rows))

        # each consumer sees its own filter applied on the shared result
        self.assertEqual(sorted(res1), [("ada", 90000), ("alan", 55000)])
        self.assertEqual(sorted(res2), [("ada",), ("alan",)])

    def test_decision_logged_for_shared_node(self):
        q1 = Filter("p_salary_over_50k", self.join)
        q2 = Filter("p_dept_eng", self.join)
        ex = Executor([self.employees, self.departments])
        ex.run_all([q1, q2])
        join_decision = ex.decisions[self.join.key()]
        self.assertEqual(join_decision.strategy, MATERIALIZE_MEMORY)
        self.assertEqual(join_decision.consumers, 2)


class TestSpill(unittest.TestCase):
    def test_spill_to_disk_when_quota_exceeded(self):
        big = relalg.Table("big", ("k", "v"),
                           [(i, i % 7) for i in range(1000)])
        register_predicate("p_even", lambda r: relalg.eq(r["k"] % 2, 0))
        shared = Filter("p_even", Scan("big"), selectivity=0.5)
        q1 = Project(("k",), shared)
        q2 = Filter("p_keep_all", shared)
        with tempfile.TemporaryDirectory() as tmp:
            catalog = Catalog(spill_dir=tmp)
            model = CostModel(mem_quota_bytes=128)  # 2 rows worth
            ex = Executor([big], catalog=catalog, model=model)
            res1, res2 = ex.run_all([q1, q2])

            d = ex.decisions[shared.key()]
            self.assertEqual(d.strategy, MATERIALIZE_SPILL)
            view = catalog.get(shared.key())
            self.assertTrue(view.spilled)
            spill_files = os.listdir(tmp)
            self.assertEqual(len(spill_files), 1)

            expected = [r for r in big.rows if r[0] % 2 == 0]
            self.assertEqual(view.read(), expected)
            self.assertEqual(res1, [(k,) for k, _ in expected])
            self.assertEqual(res2, expected)


class TestAtomicity(unittest.TestCase):
    def _failing_setup(self, spill):
        failing = FailingTable("f", ("a", "b"), [(1, 2), (3, 4)])
        register_predicate("p_any", lambda r: True)
        shared = Filter("p_any", Scan("f"))
        q1 = Project(("a",), shared)
        q2 = Project(("b",), shared)
        model = CostModel(mem_quota_bytes=1) if spill else CostModel()
        tmp = tempfile.TemporaryDirectory()
        catalog = Catalog(spill_dir=tmp.name)
        ex = Executor([failing], catalog=catalog, model=model)
        return ex, shared, tmp

    def test_failed_materialization_exposes_no_partial_table(self):
        for spill in (False, True):
            with self.subTest(spill=spill):
                ex, shared, tmp = self._failing_setup(spill)
                with self.assertRaises(RuntimeError):
                    ex.run_all([Project(("a",), shared),
                                Project(("b",), shared)])
                # no catalog entry, no half-built view visible
                self.assertIsNone(ex.catalog.get(shared.key()))
                self.assertEqual(ex.catalog.keys(), [])
                # no leftover temp/final files in the spill directory
                self.assertEqual(os.listdir(tmp.name), [])
                tmp.cleanup()

    def test_catalog_recovers_after_failure(self):
        ex, shared, tmp = self._failing_setup(spill=False)
        with self.assertRaises(RuntimeError):
            ex.run_all([Project(("a",), shared), Project(("b",), shared)])
        # replace the broken table and rerun: publish succeeds cleanly
        ex.tables["f"] = relalg.Table("f", ("a", "b"), [(1, 2), (3, 4)])
        res1, res2 = ex.run_all([Project(("a",), shared),
                                 Project(("b",), shared)])
        self.assertEqual(res1, [(1,), (3,)])
        self.assertEqual(res2, [(2,), (4,)])
        tmp.cleanup()


if __name__ == "__main__":
    unittest.main()
