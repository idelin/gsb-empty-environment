import unittest

from axcon import (Executor, SparseTensor, build_plan, dense_contract,
                   enumerate_plans)


def chain_tensors():
    a = SparseTensor("A", ("i", "j"), (100, 2),
                     {(k, k % 2): 1.0 + k for k in range(10)})
    b = SparseTensor("B", ("j", "k"), (2, 100),
                     {(k % 2, k): 2.0 + k for k in range(10)})
    c = SparseTensor("C", ("k", "l"), (100, 100),
                     {(k, k): 3.0 + k for k in range(10)})
    return a, b, c


class TestPlanner(unittest.TestCase):
    def test_three_inputs_have_multiple_orders(self):
        tensors = list(chain_tensors())
        plans = enumerate_plans(tensors, ("i", "l"))
        self.assertEqual(len(plans), 3)  # (AB)C, (BC)A, (AC)B
        ests = [p.est_intermediate_total for p in plans]
        self.assertGreater(len(set(ests)), 1)  # orders differ in cost
        self.assertEqual(ests, sorted(ests))   # cheapest first

    def test_planner_picks_smaller_intermediate(self):
        tensors = list(chain_tensors())
        plan = build_plan(tensors, ("i", "l"))
        # k is the cheap axis here: contracting B,C first wins
        self.assertEqual(plan.steps[0].contracted, ("k",))
        self.assertLess(plan.est_intermediate_total,
                        max(p.est_intermediate_total
                            for p in enumerate_plans(tensors, ("i", "l"))))

    def test_all_orders_agree_with_oracle(self):
        tensors = list(chain_tensors())
        oracle = dense_contract(tensors, ("i", "l"))
        for plan in enumerate_plans(tensors, ("i", "l")):
            result = Executor().execute(plan, tensors)
            self.assertTrue(result.completed)
            self.assertEqual(result.tensor.entries, oracle)

    def test_axis_mapping_and_stats(self):
        tensors = list(chain_tensors())
        plan = build_plan(tensors, ("i", "l"))
        self.assertEqual(plan.output.labels, ("i", "l"))
        self.assertEqual(plan.output.shape, (100, 100))
        self.assertEqual(plan.output.index_of("l"), 1)
        self.assertEqual(plan.output.size_of("i"), 100)
        self.assertIn("est_max_step", plan.stats)
        result = Executor().execute(plan, tensors)
        for key in ("completed", "chunks", "spilled_bytes",
                    "actual_intermediate_total"):
            self.assertIn(key, result.stats)
        self.assertTrue(all(s.actual_nnz is not None for s in plan.steps))


if __name__ == "__main__":
    unittest.main()
