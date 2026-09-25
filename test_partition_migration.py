"""unittest suite for typed batches, solver, planner and executor."""

import math
import unittest

from typed_batch import (ColType, NULL, TypedBatch, group_sum_int, is_nan,
                         is_null, sum_float)
from model import Cluster, Partition, Slot
from solver import InfeasibleError, solve, verify_assignment
from planner import build_plan
from executor import (ClusterMetadata, Executor, MigrationFailed, Phase,
                      StaleGenerationError)


def small_cluster(caps, spec, current=None, anti=()):
    """spec: list of (pid, weight[, allowed_slot_iterable])."""
    slots = {f"s{i}": Slot(f"s{i}", cap) for i, cap in enumerate(caps)}
    partitions = {}
    for item in spec:
        pid, weight = item[0], item[1]
        allowed = frozenset(item[2]) if len(item) > 2 else None
        partitions[pid] = Partition(pid, weight, allowed)
    current = current or {}
    return Cluster(slots=slots, partitions=partitions,
                   anti_affinity=frozenset(frozenset(p) for p in anti),
                   current=dict(current))


class TypedBatchTests(unittest.TestCase):
    def test_null_is_not_nan(self):
        batch = TypedBatch.from_rows(
            [("v", ColType.FLOAT)], [(NULL,), (float("nan"),), (1.5,)])
        values = batch.column("v")
        self.assertTrue(is_null(values[0]))
        self.assertFalse(is_nan(values[0]))
        self.assertFalse(is_null(values[1]))
        self.assertTrue(is_nan(values[1]))
        self.assertNotEqual(values[0], values[1])

    def test_null_skipped_nan_propagates(self):
        batch = TypedBatch.from_rows(
            [("v", ColType.FLOAT)], [(NULL,), (1.0,), (2.0,)])
        self.assertEqual(sum_float(batch, "v"), 3.0)
        batch_nan = TypedBatch.from_rows(
            [("v", ColType.FLOAT)], [(NULL,), (1.0,), (float("nan"),)])
        self.assertTrue(math.isnan(sum_float(batch_nan, "v")))
        batch_empty = TypedBatch.from_rows([("v", ColType.FLOAT)], [(NULL,)])
        self.assertIs(sum_float(batch_empty, "v"), NULL)

    def test_exact_integer_sum_beyond_64_bits(self):
        big = 10 ** 30
        batch = TypedBatch.from_rows(
            [("k", ColType.STRING), ("v", ColType.INT)],
            [("a", big), ("a", big + 1), ("b", 7), ("b", NULL), ("b", 8)])
        grouped = group_sum_int(batch, "k", "v")
        self.assertEqual(grouped["a"], 2 * big + 1)
        self.assertEqual(grouped["b"], 15)

    def test_int_column_rejects_float_and_bool(self):
        with self.assertRaises(TypeError):
            TypedBatch.from_rows([("v", ColType.INT)], [(1.0,)])
        with self.assertRaises(TypeError):
            TypedBatch.from_rows([("v", ColType.INT)], [(True,)])

    def test_concat_requires_matching_schema_and_skips_null_rows(self):
        a = TypedBatch.from_rows([("k", ColType.STRING), ("v", ColType.INT)],
                                 [("a", 1), ("a", NULL)])
        b = TypedBatch.from_rows([("k", ColType.STRING), ("v", ColType.INT)],
                                 [("a", 2)])
        self.assertEqual(group_sum_int(a.concat(b), "k", "v"), {"a": 3})
        wrong = TypedBatch.from_rows([("k", ColType.STRING), ("v", ColType.FLOAT)],
                                     [("a", 2.0)])
        with self.assertRaises(ValueError):
            a.concat(wrong)


class SolverTests(unittest.TestCase):
    def test_balancing_optimum_and_gap_zero(self):
        cluster = small_cluster(
            [100, 100],
            [("p1", 60), ("p2", 50), ("p3", 40), ("p4", 30)],
            current={pid: "s0" for pid in ("p1", "p2", "p3", "p4")})
        result = solve(cluster)
        self.assertTrue(result.proven_optimal)
        self.assertEqual(result.max_load, 90)
        self.assertEqual(result.max_load_lower_bound, 90)
        self.assertEqual(result.gap, 0)
        self.assertEqual(result.moved_bytes, 90)
        check = verify_assignment(cluster, result.assignment)
        self.assertTrue(check["ok"], check["errors"])
        self.assertEqual(check["max_load"], result.max_load)

    def test_affinity_and_anti_affinity(self):
        cluster = small_cluster(
            [100, 100, 100],
            [("a", 60, ["s0", "s1"]), ("b", 50), ("c", 40)],
            current={"a": "s2", "b": "s0", "c": "s1"},
            anti=[("a", "b")])
        result = solve(cluster)
        self.assertIn(result.assignment["a"], {"s0", "s1"})
        self.assertNotEqual(result.assignment["a"], result.assignment["b"])
        self.assertTrue(verify_assignment(cluster, result.assignment)["ok"])

    def test_infeasible_anti_affinity_raises(self):
        cluster = small_cluster(
            [100, 100],
            [("a", 10), ("b", 10), ("c", 10)],
            current={"a": "s0", "b": "s1", "c": "s0"},
            anti=[("a", "b"), ("a", "c"), ("b", "c")])
        with self.assertRaises(InfeasibleError):
            solve(cluster)

    def test_oversized_partition_is_unmet_not_split(self):
        cluster = small_cluster(
            [100, 100],
            [("small", 30), ("huge", 250)],
            current={"small": "s0"})
        result = solve(cluster)
        self.assertEqual(result.unmet, {"huge": 250})
        self.assertNotIn("huge", result.assignment)
        self.assertEqual(result.assignment, {"small": "s0"})
        check = verify_assignment(cluster, result.assignment)
        self.assertTrue(check["ok"])

    def test_node_limit_reports_open_gap_but_valid_assignment(self):
        cluster = small_cluster(
            [100, 60],
            [("p1", 50), ("p2", 50), ("p3", 40)],
            current={"p1": "s0", "p2": "s0", "p3": "s0"})
        result = solve(cluster, node_limit=4)
        self.assertFalse(result.proven_optimal)
        self.assertEqual(result.max_load, 90)
        self.assertEqual(result.max_load_lower_bound, 80)
        self.assertEqual(result.gap, 10)
        self.assertTrue(verify_assignment(cluster, result.assignment)["ok"])


class PlannerTests(unittest.TestCase):
    def test_rounds_respect_budget_and_plan_is_deterministic(self):
        cluster = small_cluster(
            [100, 100],
            [("p1", 60), ("p2", 50), ("p3", 40)],
            current={"p1": "s0", "p2": "s0", "p3": "s0"})
        target = {"p1": "s0", "p2": "s1", "p3": "s1"}
        plan = build_plan(cluster, target, budget_per_round=100, generation=3)
        for rnd in plan.rounds:
            self.assertLessEqual(rnd.nbytes, 100)
        self.assertEqual(
            plan.plan_id,
            build_plan(cluster, target, 100, 3).plan_id)
        self.assertNotEqual(
            plan.plan_id,
            build_plan(cluster, target, 100, 4).plan_id)

    def test_move_heavier_than_budget_is_unschedulable(self):
        cluster = small_cluster(
            [1000, 1000],
            [("big", 500)], current={"big": "s0"})
        plan = build_plan(cluster, {"big": "s1"}, budget_per_round=100,
                          generation=1)
        self.assertEqual(len(plan.rounds), 0)
        self.assertEqual([m.pid for m in plan.unschedulable], ["big"])


class ExecutorTests(unittest.TestCase):
    def _setup(self, budget=100):
        cluster = small_cluster(
            [150, 150],
            [("p1", 60), ("p2", 50), ("p3", 40)],
            current={"p1": "s0", "p2": "s0", "p3": "s0"})
        target = {"p1": "s0", "p2": "s1", "p3": "s1"}
        plan = build_plan(cluster, target, budget, generation=5)
        meta = ClusterMetadata.from_initial(cluster)
        return cluster, plan, meta, target

    def test_happy_path_and_budget_refunded(self):
        _, plan, meta, target = self._setup()
        ex = Executor(meta, plan.budget_per_round)
        ex.execute(plan)
        self.assertEqual(meta.ownership, target)
        self.assertTrue(ex.consistency_report()["ok"])
        self.assertEqual({pid for pid in meta.data["s1"]}, {"p2", "p3"})
        self.assertEqual({pid for pid in meta.data["s0"]}, {"p1"})
        self.assertTrue(all(v == 0 for v in meta.budget_ledger.values()))
        self.assertTrue(all(not r for r in meta.reservations.values()))

    def test_failure_at_copy_rolls_back_without_double_owner(self):
        cluster, plan, meta, _ = self._setup()
        ex = Executor(meta, plan.budget_per_round)
        victim = plan.rounds[0].moves[0]
        with self.assertRaises(MigrationFailed):
            ex.execute(plan, fail_at=(victim.pid, Phase.COPY))
        self.assertEqual(meta.ownership[victim.pid], victim.src)
        self.assertNotIn(victim.pid, meta.data[victim.dst])
        self.assertFalse(any(meta.reservations[s] for s in meta.reservations))
        self.assertTrue(all(v == 0 for v in meta.budget_ledger.values()))
        report = ex.consistency_report()
        self.assertTrue(report["ok"], report["errors"])

    def test_failure_after_switch_rolls_ownership_back(self):
        _, plan, meta, _ = self._setup()
        ex = Executor(meta, plan.budget_per_round)
        victim = plan.rounds[0].moves[0]
        with self.assertRaises(MigrationFailed):
            ex.execute(plan, fail_at=(victim.pid, Phase.SWITCH))
        self.assertEqual(meta.ownership[victim.pid], victim.src)
        self.assertTrue(ex.consistency_report()["ok"])

    def test_stale_ack_rejected_by_generation(self):
        _, plan, meta, _ = self._setup()
        ex = Executor(meta, plan.budget_per_round)
        ex.execute(plan)
        with self.assertRaises(StaleGenerationError):
            ex.handle_completion_ack("p2", generation=plan.generation - 1)
        self.assertEqual(ex.handle_completion_ack("p2", plan.generation),
                         "accepted")

    def test_replay_same_plan_does_not_double_occupy(self):
        cluster, plan, meta, target = self._setup()
        ex = Executor(meta, plan.budget_per_round)
        ex.execute(plan)
        loads_after = {sid: meta.data_bytes(sid) for sid in meta.capacities}
        outcomes = ex.execute(plan)  # replay identical plan_id
        self.assertTrue(all(o == "skipped-already-complete"
                            for _, o in outcomes))
        self.assertEqual(meta.ownership, target)
        self.assertEqual({sid: meta.data_bytes(sid)
                          for sid in meta.capacities}, loads_after)
        self.assertTrue(all(not r for r in meta.reservations.values()))
        self.assertTrue(ex.consistency_report()["ok"])

    def test_older_plan_generation_refused(self):
        _, plan, meta, _ = self._setup()
        ex = Executor(meta, plan.budget_per_round)
        ex.execute(plan)
        older = build_plan(small_cluster(
            [100, 100], [("p1", 60)], current={"p1": "s0"}),
            {"p1": "s1"}, 100, generation=plan.generation - 1)
        with self.assertRaises(StaleGenerationError):
            ex.execute(older)

    def test_reservation_respects_slot_capacity(self):
        _, plan, meta, _ = self._setup(budget=100)
        ex = Executor(meta, plan.budget_per_round)
        # corrupt target free space so the first reserve must fail
        meta.reservations["s1"]["(manual, padding)"] = (120, 99)
        with self.assertRaises(Exception):
            ex.execute(plan)


if __name__ == "__main__":
    unittest.main(verbosity=2)
