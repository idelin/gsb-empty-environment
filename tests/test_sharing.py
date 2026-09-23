"""共享正确性：不同过滤不串扰、物化失败不暴露半表、溢写往返一致。"""

import os
import unittest

from subplan.costing import MAT_MEMORY, MAT_SPILL, RECOMPUTE, CostModel
from subplan.engine import Engine
from subplan.plan import Filter, Join, Node, Scan, largest_common_subplan
from subplan.relation import Table


def build_queries():
    emp = Table('emp', ('id', 'name', 'dept', 'salary'), [
        (1, 'alice', 10, 200),
        (2, 'bob', 10, 120),
        (3, 'carol', 20, 300),
        (4, 'dave', None, 180),
    ])
    dept = Table('dept', ('did', 'dname'), [
        (10, 'Engineering'),
        (20, 'Sales'),
    ])
    shared = Join(Scan(emp), Scan(dept), 2, 0)
    # 两个消费者在同一公共子计划上挂不同过滤
    q1 = Filter(shared, lambda r: r[3] >= 150, label='sal>=150')
    q2 = Filter(shared, lambda r: r[5] == 'Engineering', label='dept=Eng')
    return shared, q1, q2


class TestSharingCorrectness(unittest.TestCase):
    def test_common_subplan_is_below_filters(self):
        shared, q1, q2 = build_queries()
        common = largest_common_subplan(q1, q2)
        self.assertEqual(common.signature(), shared.signature())

    def test_two_consumers_different_filters(self):
        shared, q1, q2 = build_queries()
        engine = Engine(memory_budget=1 << 30)
        decision, iterators = engine.run_shared(shared, consumers=2)
        self.assertEqual(decision.strategy, MAT_MEMORY)

        # 每个消费者在**未过滤**的共享结果上各自做自己的过滤
        result1 = [r for r in iterators[0] if q1.predicate(r) is True]
        result2 = [r for r in iterators[1] if q2.predicate(r) is True]

        self.assertEqual(sorted(r[1] for r in result1),
                         ['alice', 'carol'])
        self.assertEqual(sorted(r[1] for r in result2),
                         ['alice', 'bob'])

        # 物化的是未过滤的完整公共结果（dave 因 NULL 连接键缺席，共 3 行）
        materialized = list(engine.store.get(shared.signature()))
        self.assertEqual(len(materialized), 3)
        self.assertNotIn('dave', [r[1] for r in materialized])

        # 两个消费者都不会把对方的过滤条件串过来：
        # bob 薪水 120 < 150，若 q1 的过滤串到 q2 则 bob 会缺席；
        # carol 在 Sales，若 q2 的过滤串到 q1 则 carol 会缺席。
        self.assertIn('bob', [r[1] for r in result2])
        self.assertIn('carol', [r[1] for r in result1])

    def test_single_consumer_recomputes_and_does_not_store(self):
        shared, _, _ = build_queries()
        engine = Engine(memory_budget=1 << 30)
        decision, iterators = engine.run_shared(shared, consumers=1)
        self.assertEqual(decision.strategy, RECOMPUTE)
        self.assertEqual(len(list(iterators[0])), 3)
        self.assertNotIn(shared.signature(), engine.store)


class _ExplodingNode(Node):
    """产出前 2 行后抛出异常的子计划，模拟物化中途失败。"""

    def signature(self):
        return ('exploding',)

    def estimate_rows(self):
        return 10

    def row_width(self):
        return 16

    def compute_cost(self, model):
        return 10

    def iter_rows(self):
        def generate():
            yield (1,)
            yield (2,)
            raise RuntimeError('materialization boom')
        return generate()


class TestAtomicMaterialization(unittest.TestCase):
    # 廉价的溢写代价模型，确保溢写方案在代价上胜出从而真正走溢写路径
    CHEAP_SPILL = CostModel(spill_write_byte=0.001, spill_read_byte=0.001)

    def setUp(self):
        self.node = _ExplodingNode()

    def _assert_half_table_never_visible(self, engine, spilled):
        with self.assertRaises(RuntimeError):
            engine.run_shared(self.node, consumers=3)
        # 存储中不存在该条目，读者拿不到"半张表"
        self.assertNotIn(self.node.signature(), engine.store)
        with self.assertRaises(KeyError):
            list(engine.store.get(self.node.signature()))
        if spilled:
            leftovers = [f for f in os.listdir(engine.store.spill_dir)
                         if f.startswith('mat_')]
            self.assertEqual(leftovers, [])

    def test_memory_failure_is_atomic(self):
        engine = Engine(memory_budget=1 << 30)
        self._assert_half_table_never_visible(engine, spilled=False)

    def test_spill_failure_is_atomic(self):
        engine = Engine(memory_budget=1, model=self.CHEAP_SPILL)
        self._assert_half_table_never_visible(engine, spilled=True)


class TestSpillRoundTrip(unittest.TestCase):
    def test_spill_results_equal_recompute(self):
        shared, q1, q2 = build_queries()
        # 极小预算 + 廉价溢写模型 → 溢写物化在代价上胜出
        model = CostModel(spill_write_byte=0.001, spill_read_byte=0.001)
        engine = Engine(memory_budget=1, model=model)
        decision, iterators = engine.run_shared(shared, consumers=2)
        self.assertEqual(decision.strategy, MAT_SPILL)

        result1 = [r for r in iterators[0] if q1.predicate(r) is True]
        result2 = [r for r in iterators[1] if q2.predicate(r) is True]
        expected1 = list(q1.iter_rows())
        expected2 = list(q2.iter_rows())
        self.assertEqual(sorted(result1), sorted(expected1))
        self.assertEqual(sorted(result2), sorted(expected2))


if __name__ == '__main__':
    unittest.main()
