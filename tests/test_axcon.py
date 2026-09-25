"""Unittest suite for axcon: validation, planning, deltas, spill/resume."""

import itertools
import os
import random
import shutil
import tempfile
import unittest

from axcon import (Budget, BudgetExceeded, ContractionCache, Delta,
                   SparseTensor, StepSpec, Plan, ValidationError,
                   contract_many, contract_pair, dense_contract, plan_order,
                   Operand)


def rand_tensor(name, axes, shape, density=0.7, seed=0, lo=1, hi=4):
    rng = random.Random(seed * 1000 + sum(map(ord, name)))
    entries = {}
    for coord in itertools.product(*[range(shape[l]) for l in axes]):
        if rng.random() < density:
            entries[coord] = rng.randint(lo, hi)
    return SparseTensor(name, axes, shape, entries=entries)


def entries_of(tensor):
    return dict(tensor.iter_entries())


class TestValidation(unittest.TestCase):
    def test_negative_dimension_rejected(self):
        with self.assertRaises(ValidationError):
            SparseTensor("A", ("i",), {"i": -1})

    def test_missing_dimension_rejected(self):
        with self.assertRaises(ValidationError):
            SparseTensor("A", ("i",), {})

    def test_coordinate_out_of_range_rejected(self):
        t = SparseTensor("A", ("i",), {"i": 3})
        with self.assertRaises(ValidationError):
            t.set((3,), 1.0)
        with self.assertRaises(ValidationError):
            t.apply_delta(Delta("d1", (-1,), 1.0))

    def test_wrong_arity_coordinate_rejected(self):
        t = SparseTensor("A", ("i", "j"), {"i": 2, "j": 2})
        with self.assertRaises(ValidationError):
            t.set((0,), 1.0)

    def test_shared_label_size_mismatch_rejected(self):
        a = SparseTensor("A", ("i",), {"i": 3}, entries={(0,): 1})
        b = SparseTensor("B", ("i",), {"i": 4}, entries={(0,): 1})
        with self.assertRaises(ValidationError):
            contract_many([a, b], ())

    def test_unknown_output_label_rejected(self):
        a = SparseTensor("A", ("i",), {"i": 3}, entries={(0,): 1})
        with self.assertRaises(ValidationError):
            contract_many([a], ("zzz",))

    def test_duplicate_output_label_rejected(self):
        a = SparseTensor("A", ("i",), {"i": 3}, entries={(0,): 1})
        with self.assertRaises(ValidationError):
            contract_many([a], ("i", "i"))

    def test_empty_axis_yields_empty_result(self):
        a = SparseTensor("A", ("i",), {"i": 0})
        b = SparseTensor("B", ("i",), {"i": 0})
        res = contract_many([a, b], ())
        self.assertTrue(res.complete)
        self.assertEqual(res.tensor.nnz, 0)
        self.assertEqual(dense_contract([a, b], ()), {})

    def test_scalar_tensors(self):
        s = SparseTensor("s", (), {}, entries={(): 3})
        v = SparseTensor("v", ("i",), {"i": 2}, entries={(0,): 2, (1,): 5})
        res = contract_many([s, v], ("i",))
        self.assertEqual(entries_of(res.tensor), {(0,): 6, (1,): 15})
        self.assertEqual(entries_of(res.tensor), dense_contract([s, v], ("i",)))
        self.assertEqual(res.axis_mapping, {"i": 0})

    def test_scalar_result(self):
        a = SparseTensor("A", ("i",), {"i": 2}, entries={(0,): 1, (1,): 2})
        b = SparseTensor("B", ("i",), {"i": 2}, entries={(0,): 3, (1,): 4})
        res = contract_many([a, b], ())
        self.assertEqual(entries_of(res.tensor), {(): 11})
        self.assertEqual(res.axis_mapping, {})


class TestContractionSemantics(unittest.TestCase):
    def test_repeated_label_diagonal(self):
        t = SparseTensor("T", ("i", "i"), {"i": 3},
                         entries={(0, 0): 2, (1, 1): 3, (0, 1): 9, (2, 2): 5})
        m = SparseTensor("M", ("i", "j"), {"i": 3, "j": 2},
                         entries={(0, 0): 1, (1, 1): 1, (2, 0): 1})
        res = contract_many([t, m], ("j",))
        self.assertEqual(entries_of(res.tensor), dense_contract([t, m], ("j",)))
        self.assertEqual(entries_of(res.tensor), {(0,): 7, (1,): 3})

    def test_broadcast_outer_product(self):
        a = SparseTensor("A", ("i",), {"i": 2}, entries={(0,): 2, (1,): 3})
        b = SparseTensor("B", ("j",), {"j": 3}, entries={(0,): 1, (2,): 4})
        res = contract_many([a, b], ("i", "j"))
        expected = {(0, 0): 2, (0, 2): 8, (1, 0): 3, (1, 2): 12}
        self.assertEqual(entries_of(res.tensor), expected)
        self.assertEqual(entries_of(res.tensor), dense_contract([a, b], ("i", "j")))

    def test_canceling_contributions_dropped(self):
        a = SparseTensor("A", ("i",), {"i": 2}, entries={(0,): 2, (1,): -2})
        b = SparseTensor("B", ("i",), {"i": 2}, entries={(0,): 3, (1,): 3})
        res = contract_many([a, b], ())
        self.assertEqual(res.tensor.nnz, 0)  # 2*3 + (-2)*3 == 0 -> dropped

    def test_same_coordinate_deltas_cancel(self):
        t = SparseTensor("A", ("i",), {"i": 4}, entries={(1,): 7})
        t.apply_delta(Delta("plus", (1,), 5))
        t.apply_delta(Delta("minus", (1,), -5))
        self.assertEqual(t.get((1,)), 7)
        t.apply_delta(Delta("wipe", (1,), -7))
        self.assertEqual(t.nnz, 0)

    def test_random_three_input_matches_oracle(self):
        a = rand_tensor("A", ("i", "k"), {"i": 3, "k": 4}, seed=1)
        b = rand_tensor("B", ("k", "j"), {"k": 4, "j": 3}, seed=2)
        c = rand_tensor("C", ("j", "i"), {"j": 3, "i": 3}, seed=3)
        res = contract_many([a, b, c], ("i", "j"))
        self.assertTrue(res.complete)
        self.assertEqual(entries_of(res.tensor), dense_contract([a, b, c], ("i", "j")))


class TestPlanning(unittest.TestCase):
    def setUp(self):
        # Shapes chosen so (A*B) is tiny but (B*C) is huge.
        self.a = rand_tensor("A", ("i", "k"), {"i": 2, "k": 40}, density=0.9, seed=1)
        self.b = rand_tensor("B", ("k", "j"), {"k": 40, "j": 2}, density=0.9, seed=2)
        self.c = rand_tensor("C", ("j", "m"), {"j": 2, "m": 30}, density=0.9, seed=3)

    def test_two_orders_agree_but_intermediates_differ(self):
        oracle = dense_contract([self.a, self.b, self.c], ("i", "m"))
        order_ab = Plan(steps=[
            StepSpec("A", "B", "(A*B)", ("i", "j"), 0),
            StepSpec("(A*B)", "C", "((A*B)*C)", ("i", "m"), 0),
        ], total_est_nnz=0, orders_considered=2)
        order_bc = Plan(steps=[
            StepSpec("B", "C", "(B*C)", ("k", "m"), 0),
            StepSpec("A", "(B*C)", "(A*(B*C))", ("i", "m"), 0),
        ], total_est_nnz=0, orders_considered=2)
        res_ab = contract_many([self.a, self.b, self.c], ("i", "m"), plan=order_ab)
        res_bc = contract_many([self.a, self.b, self.c], ("i", "m"), plan=order_bc)
        self.assertEqual(entries_of(res_ab.tensor), oracle)
        self.assertEqual(entries_of(res_bc.tensor), oracle)
        self.assertLess(res_ab.stats.actual_intermediate_nnz,
                        res_bc.stats.actual_intermediate_nnz)

    def test_planner_picks_smaller_intermediate(self):
        plan = plan_order([Operand.from_tensor(t) for t in (self.a, self.b, self.c)],
                          ("i", "m"))
        self.assertEqual(plan.steps[0].left, "A")
        self.assertEqual(plan.steps[0].right, "B")
        self.assertGreaterEqual(plan.orders_considered, 2)

    def test_axis_mapping_in_result(self):
        res = contract_many([self.a, self.b, self.c], ("i", "m"))
        self.assertEqual(res.axis_mapping, {"i": 0, "m": 1})
        self.assertEqual(res.tensor.axes, ("i", "m"))


class TestDeltasAndCache(unittest.TestCase):
    def test_delta_marks_only_containing_chunk_dirty(self):
        t = SparseTensor("A", ("i",), {"i": 6}, block_size=2,
                         entries={(0,): 1, (2,): 1, (4,): 1})
        t.clear_dirty()
        t.apply_delta(Delta("d1", (5,), 3))
        self.assertEqual(t.dirty_chunks, {(2,)})
        t.clear_dirty()
        t.apply_delta(Delta("d2", (1,), 3))
        self.assertEqual(t.dirty_chunks, {(0,)})

    def test_duplicate_delta_id_not_reapplied(self):
        t = SparseTensor("A", ("i",), {"i": 3}, entries={(0,): 1})
        delta = Delta("same", (0,), 10)
        self.assertTrue(t.apply_delta(delta))
        self.assertFalse(t.apply_delta(delta))
        self.assertEqual(t.get((0,)), 11)
        self.assertEqual(t.version, 1)

    def test_delta_bumps_version_and_invalidates_cache(self):
        a = SparseTensor("A", ("i",), {"i": 3}, entries={(0,): 1, (1,): 2})
        b = SparseTensor("B", ("i",), {"i": 3}, entries={(0,): 3, (1,): 4})
        cache = ContractionCache()
        r1 = contract_many([a, b], (), cache=cache)
        self.assertEqual(r1.stats.cache_hits, 0)
        r2 = contract_many([a, b], (), cache=cache)
        self.assertEqual(r2.stats.cache_hits, 1)
        self.assertEqual(entries_of(r1.tensor), entries_of(r2.tensor))
        a.apply_delta(Delta("bump", (0,), 100))
        r3 = contract_many([a, b], (), cache=cache)
        self.assertEqual(r3.stats.cache_hits, 0)  # version changed -> miss
        self.assertEqual(entries_of(r3.tensor), dense_contract([a, b], ()))


class TestSpillCheckpoint(unittest.TestCase):
    def setUp(self):
        self.workdir = tempfile.mkdtemp(prefix="axcon_test_")
        self.addCleanup(shutil.rmtree, self.workdir, True)
        self.a = rand_tensor("A", ("i", "k"), {"i": 5, "k": 5},
                             density=0.8, seed=11)
        self.b = rand_tensor("B", ("k", "j"), {"k": 5, "j": 5},
                             density=0.8, seed=12)

    def test_spill_forced_by_small_buffer(self):
        budget = Budget(buffer_entries=2)
        res, stats = contract_pair(self.a, self.b, ("i", "j"), budget=budget,
                                   workdir=os.path.join(self.workdir, "s1"))
        self.assertGreater(stats.spill_files, 0)
        self.assertEqual(entries_of(res), dense_contract([self.a, self.b], ("i", "j")))

    def test_crash_and_resume_no_double_apply(self):
        budget = Budget(buffer_entries=2)
        step_dir = os.path.join(self.workdir, "s2")
        calls = {"n": 0}

        def crash_after_two(_key):
            calls["n"] += 1
            if calls["n"] == 2:
                raise RuntimeError("simulated crash")

        with self.assertRaises(RuntimeError):
            contract_pair(self.a, self.b, ("i", "j"), budget=budget,
                          workdir=step_dir, chunk_hook=crash_after_two)
        self.assertTrue(os.path.exists(os.path.join(step_dir, "checkpoint.json")))
        res, stats = contract_pair(self.a, self.b, ("i", "j"), budget=budget,
                                   workdir=step_dir)
        self.assertTrue(stats.resumed)
        self.assertGreater(stats.chunks_skipped, 0)
        # Values would double if committed chunks were re-applied.
        self.assertEqual(entries_of(res), dense_contract([self.a, self.b], ("i", "j")))
        self.assertFalse(os.path.exists(os.path.join(step_dir, "checkpoint.json")))

    def test_resume_after_delta_restarts_cleanly(self):
        budget = Budget(buffer_entries=2)
        step_dir = os.path.join(self.workdir, "s3")
        calls = {"n": 0}

        def crash(_key):
            calls["n"] += 1
            if calls["n"] == 1:
                raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            contract_pair(self.a, self.b, ("i", "j"), budget=budget,
                          workdir=step_dir, chunk_hook=crash)
        self.a.apply_delta(Delta("late", (0, 0), 5))  # version bump
        res, stats = contract_pair(self.a, self.b, ("i", "j"), budget=budget,
                                   workdir=step_dir)
        self.assertFalse(stats.resumed)  # stale checkpoint discarded
        self.assertEqual(entries_of(res), dense_contract([self.a, self.b], ("i", "j")))

    def test_budget_abort_marks_plan_incomplete(self):
        budget = Budget(max_contributions=3)
        res = contract_many([self.a, self.b], ("i", "j"), budget=budget,
                            workdir=os.path.join(self.workdir, "s4"))
        self.assertFalse(res.complete)
        self.assertIsNone(res.tensor)
        self.assertIn("budget", res.error)
        self.assertGreater(res.stats.contributions, 0)


if __name__ == "__main__":
    unittest.main()
