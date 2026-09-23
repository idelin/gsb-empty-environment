"""物化 / 重算 / 溢写决策测试。"""

import unittest

from subplan.costing import (MAT_MEMORY, MAT_SPILL, RECOMPUTE, CostModel,
                             decide)
from subplan.plan import Join, Scan
from subplan.relation import Table


def big_join(rows=1000):
    left = Table('l', ('a', 'b'), [(i, i) for i in range(rows)])
    right = Table('r', ('a', 'c'), [(i, i) for i in range(rows)])
    return Join(Scan(left), Scan(right), 0, 0, estimate=rows)


class TestDecision(unittest.TestCase):
    def test_single_consumer_never_materializes(self):
        decision = decide(big_join(), consumers=1, memory_budget=1 << 30)
        self.assertEqual(decision.strategy, RECOMPUTE)
        self.assertIn('1 次读取', decision.rationale())

    def test_many_consumers_materialize_in_memory(self):
        decision = decide(big_join(), consumers=5, memory_budget=1 << 30)
        self.assertEqual(decision.strategy, MAT_MEMORY)
        self.assertTrue(decision.fits_in_memory)
        self.assertLess(decision.memory_cost, decision.recompute_cost)

    def test_over_budget_spills_to_disk(self):
        node = big_join()
        tiny_budget = node.estimate_rows() * node.row_width() - 1
        decision = decide(node, consumers=5, memory_budget=tiny_budget)
        self.assertEqual(decision.strategy, MAT_SPILL)
        self.assertFalse(decision.fits_in_memory)
        self.assertIsNone(decision.memory_cost)
        self.assertIn('需溢写', decision.rationale())

    def test_expensive_write_falls_back_to_recompute(self):
        model = CostModel(mem_write_byte=10.0, mem_read_byte=10.0,
                          mem_hold_byte=10.0, spill_write_byte=10.0,
                          spill_read_byte=10.0)
        decision = decide(big_join(), consumers=2,
                          memory_budget=1 << 30, model=model)
        self.assertEqual(decision.strategy, RECOMPUTE)

    def test_rationale_lists_all_costs(self):
        decision = decide(big_join(), consumers=3, memory_budget=1 << 30)
        text = decision.rationale()
        self.assertIn('重算', text)
        self.assertIn('内存物化', text)
        self.assertIn('溢写物化', text)
        self.assertIn(f"{decision.recompute_cost:.1f}", text)


if __name__ == '__main__':
    unittest.main()
