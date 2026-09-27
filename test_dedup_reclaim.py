#!/usr/bin/env python3
"""dedup_reclaim 的单元测试。所有数据均为合成数据。"""

import itertools
import random
import unittest

from dedup_reclaim import (
    InstanceError,
    load_instance,
    max_reclaimable,
    reclaimed_bytes,
    solve,
)


def make_instance(blocks, snapshots, required, versions=None):
    data = {"blocks": blocks, "snapshots": snapshots,
            "required_bytes": required}
    if versions is not None:
        data["versions"] = versions
    return load_instance(data)


def brute_force(inst):
    """指数级暴力枚举，作为精确解的参照。"""
    ids = sorted(inst.snap_losses)
    best = None
    for r in range(len(ids) + 1):
        for combo in itertools.combinations(ids, r):
            if reclaimed_bytes(inst, combo) < inst.required_bytes:
                continue
            loss = sum(inst.snap_losses[s] for s in combo)
            key = (loss, len(combo), combo)
            if best is None or key < best[0]:
                best = (key, list(combo))
    return best


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界：两个快照各引用同一个 10 字节块。"""

    def setUp(self):
        self.inst = make_instance(
            blocks={"b1": 10},
            versions={"v1": ["b1"]},
            snapshots=[
                {"id": "s1", "loss": 4, "versions": ["v1"]},
                {"id": "s2", "loss": 6, "versions": ["v1"]},
            ],
            required=10,
        )

    def test_delete_either_alone_reclaims_zero(self):
        self.assertEqual(reclaimed_bytes(self.inst, ["s1"]), 0)
        self.assertEqual(reclaimed_bytes(self.inst, ["s2"]), 0)

    def test_delete_both_reclaims_ten(self):
        self.assertEqual(reclaimed_bytes(self.inst, ["s1", "s2"]), 10)

    def test_individual_amounts_not_additive(self):
        # 单独可回收量之和为 0+0=0，但联合删除可回收 10。
        individual_sum = (reclaimed_bytes(self.inst, ["s1"])
                          + reclaimed_bytes(self.inst, ["s2"]))
        self.assertEqual(individual_sum, 0)
        self.assertEqual(reclaimed_bytes(self.inst, ["s1", "s2"]), 10)

    def test_solver_must_delete_both(self):
        res = solve(self.inst)
        self.assertEqual(res.status, "optimal")
        self.assertTrue(res.optimal_proven)
        self.assertEqual(res.selected, ["s1", "s2"])
        self.assertEqual(res.total_loss, 10)
        self.assertEqual(res.reclaimed_bytes, 10)
        self.assertEqual(res.reclaimed_blocks, ["b1"])


class SolverCorrectnessTest(unittest.TestCase):

    def test_required_zero_needs_no_deletion(self):
        inst = make_instance({"b1": 5},
                             [{"id": "s1", "loss": 1, "blocks": ["b1"]}],
                             required=0)
        res = solve(inst)
        self.assertEqual(res.status, "optimal")
        self.assertEqual(res.selected, [])
        self.assertEqual(res.total_loss, 0)

    def test_infeasible_when_required_exceeds_total(self):
        inst = make_instance({"b1": 5},
                             [{"id": "s1", "loss": 1, "blocks": ["b1"]}],
                             required=6)
        res = solve(inst)
        self.assertEqual(res.status, "infeasible")
        self.assertTrue(res.optimal_proven)
        self.assertEqual(max_reclaimable(inst), 5)

    def test_tie_break_fewer_deletions(self):
        # A 独自回收 10（损失 5）；B+C 合计回收 10（损失也是 5）→ 选 A。
        inst = make_instance(
            {"ba": 10, "bb": 6, "bc": 4},
            [{"id": "A", "loss": 5, "blocks": ["ba"]},
             {"id": "B", "loss": 2, "blocks": ["bb"]},
             {"id": "C", "loss": 3, "blocks": ["bc"]}],
            required=10)
        res = solve(inst)
        self.assertEqual(res.selected, ["A"])
        self.assertEqual(res.total_loss, 5)

    def test_tie_break_lexicographic_ids(self):
        inst = make_instance(
            {"b1": 10, "b2": 10},
            [{"id": "snap-b", "loss": 5, "blocks": ["b1"]},
             {"id": "snap-a", "loss": 5, "blocks": ["b2"]}],
            required=10)
        res = solve(inst)
        self.assertEqual(res.selected, ["snap-a"])

    def test_complementarity_chain(self):
        # s1:{b1} s2:{b1,b2} s3:{b2}；只有三者全删才能回收 12。
        inst = make_instance(
            {"b1": 6, "b2": 6},
            [{"id": "s1", "loss": 1, "blocks": ["b1"]},
             {"id": "s2", "loss": 1, "blocks": ["b1", "b2"]},
             {"id": "s3", "loss": 1, "blocks": ["b2"]}],
            required=12)
        self.assertEqual(reclaimed_bytes(inst, ["s1", "s2"]), 6)
        self.assertEqual(reclaimed_bytes(inst, ["s2", "s3"]), 6)
        res = solve(inst)
        self.assertEqual(res.selected, ["s1", "s2", "s3"])
        self.assertEqual(res.reclaimed_bytes, 12)

    def test_shared_file_version(self):
        # 两个快照引用同一个文件版本，块通过版本共享。
        inst = make_instance(
            {"b1": 7},
            versions={"v1": ["b1"]},
            snapshots=[{"id": "s1", "loss": 1, "versions": ["v1"]},
                       {"id": "s2", "loss": 1, "versions": ["v1"]}],
            required=7)
        res = solve(inst)
        self.assertEqual(res.selected, ["s1", "s2"])

    def test_picks_cheapest_not_most_reclaiming(self):
        inst = make_instance(
            {"b1": 20, "b2": 10},
            [{"id": "big", "loss": 9, "blocks": ["b1"]},
             {"id": "small", "loss": 1, "blocks": ["b2"]}],
            required=10)
        res = solve(inst)
        self.assertEqual(res.selected, ["small"])
        self.assertEqual(res.reclaimed_bytes, 10)


class BudgetAndLimitTest(unittest.TestCase):

    def setUp(self):
        self.inst = make_instance(
            {"b1": 10, "b2": 10},
            [{"id": "cheap", "loss": 3, "blocks": ["b1"]},
             {"id": "dear", "loss": 8, "blocks": ["b2"]}],
            required=10)

    def test_budget_allows_optimal(self):
        res = solve(self.inst, loss_budget=3)
        self.assertEqual(res.status, "optimal")
        self.assertEqual(res.selected, ["cheap"])

    def test_budget_exceeded(self):
        res = solve(self.inst, loss_budget=2)
        self.assertEqual(res.status, "budget_exceeded")
        self.assertEqual(res.selected, [])

    def test_node_limit_marks_unproven(self):
        res = solve(self.inst, max_nodes=1)
        self.assertFalse(res.optimal_proven)
        self.assertIn(res.status, ("feasible", "unknown"))


class InputValidationTest(unittest.TestCase):

    def test_unknown_block_rejected(self):
        with self.assertRaises(InstanceError):
            make_instance({"b1": 1}, [{"id": "s", "blocks": ["nope"]}], 1)

    def test_unknown_version_rejected(self):
        with self.assertRaises(InstanceError):
            make_instance({"b1": 1}, [{"id": "s", "versions": ["v?"]}], 1,
                          versions={"v1": ["b1"]})

    def test_duplicate_snapshot_rejected(self):
        with self.assertRaises(InstanceError):
            make_instance({"b1": 1},
                          [{"id": "s", "blocks": ["b1"]},
                           {"id": "s", "blocks": ["b1"]}], 1)

    def test_negative_loss_rejected(self):
        with self.assertRaises(InstanceError):
            make_instance({"b1": 1}, [{"id": "s", "loss": -1, "blocks": ["b1"]}], 1)


class ExactVsBruteForceTest(unittest.TestCase):
    """随机合成小实例上，分支定界结果必须与暴力枚举完全一致。"""

    def test_random_instances_match_brute_force(self):
        rng = random.Random(20260928)
        for trial in range(300):
            n_blocks = rng.randint(1, 5)
            n_snaps = rng.randint(1, 8)
            blocks = {f"b{j}": rng.randint(1, 9) for j in range(n_blocks)}
            snaps = []
            for i in range(n_snaps):
                chosen = {b for b in blocks if rng.random() < 0.5}
                snaps.append({"id": f"s{i}",
                              "loss": rng.randint(0, 9),
                              "blocks": sorted(chosen)})
            required = rng.randint(0, sum(blocks.values()) + 3)
            inst = make_instance(blocks, snaps, required)
            res = solve(inst)
            ref = brute_force(inst)
            if ref is None:
                self.assertEqual(res.status, "infeasible",
                                 f"trial {trial}: 应为不可行")
            else:
                (exp_loss, exp_count, exp_ids), _ = ref
                self.assertEqual(res.status, "optimal", f"trial {trial}")
                self.assertTrue(res.optimal_proven)
                self.assertEqual(tuple(res.selected), exp_ids, f"trial {trial}")
                self.assertEqual(res.total_loss, exp_loss, f"trial {trial}")
                self.assertEqual(len(res.selected), exp_count, f"trial {trial}")
                # 回收量必须真实满足约束（由引用计数重算，非简单相加）
                self.assertGreaterEqual(
                    reclaimed_bytes(inst, res.selected), required)


if __name__ == "__main__":
    unittest.main()
