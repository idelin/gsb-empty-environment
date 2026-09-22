"""releng 引擎的 unittest 测试套件。运行：python3 -m unittest discover -s tests -v"""
import unittest

from releng import (
    Budget,
    Filter,
    HashAggregate,
    HashJoin,
    Limit,
    NestedLoopJoin,
    Project,
    ResourceRegistry,
    Scan,
    Sort,
    Table,
    TerminationReason,
    execute,
    make_bulk_table,
    orders,
    sql_eq,
    sql_gt,
    users,
)


class TestFullExecution(unittest.TestCase):
    def test_scan_full_completes(self):
        result = execute(Scan(users))
        self.assertTrue(result.complete)
        self.assertEqual(result.termination, TerminationReason.EXHAUSTED)
        self.assertFalse(result.partial_aggregates)
        self.assertEqual(len(result.rows), 8)
        self.assertEqual(result.stats.rows_processed, 8)
        self.assertEqual(result.stats.steps, 8)

    def test_hash_join_correctness(self):
        plan = HashJoin(Scan(users), Scan(orders), "user_id", "user_id")
        result = execute(plan)
        self.assertTrue(result.complete)
        # 匹配明细：u1x2, u2x2(103,111), u3x2, u5: 2用户x2订单=4, u4x1, u6x1
        self.assertEqual(len(result.rows), 12)
        for row in result.rows:
            self.assertIsNotNone(row["user_id"])
            self.assertEqual(row["user_id"], row["right.user_id"])

    def test_memory_released_after_join(self):
        budget = Budget()
        plan = HashJoin(Scan(users), Scan(orders), "user_id", "user_id")
        result = execute(plan, budget)
        self.assertTrue(result.complete)
        self.assertGreater(result.stats.peak_memory, 0)
        self.assertEqual(budget.used_memory, 0)  # 构建侧内存全部归还


class TestBudgetInterruption(unittest.TestCase):
    def test_row_budget_partial(self):
        budget = Budget(max_rows=3)
        result = execute(Scan(users), budget)
        self.assertFalse(result.complete)
        self.assertEqual(result.termination, TerminationReason.BUDGET_ROWS)
        self.assertEqual(len(result.rows), 3)            # 部分结果照常返回
        self.assertEqual(result.stats.rows_processed, 4)  # 越界的那次也记账

    def test_step_budget_partial(self):
        result = execute(Scan(users), Budget(max_steps=5))
        self.assertFalse(result.complete)
        self.assertEqual(result.termination, TerminationReason.BUDGET_STEPS)
        self.assertEqual(len(result.rows), 5)

    def test_memory_budget_hash_join(self):
        result = execute(
            HashJoin(Scan(users), Scan(orders), "user_id", "user_id"),
            Budget(max_memory=100),
        )
        self.assertFalse(result.complete)
        self.assertEqual(result.termination, TerminationReason.BUDGET_MEMORY)
        self.assertEqual(result.rows, [])

    def test_aggregate_partial_never_masquerades_as_final(self):
        plan = HashAggregate(
            Scan(orders),
            group_by=["status"],
            aggregates=[("count_star", None, "n"), ("sum", "amount", "total")],
        )
        full = execute(plan)
        self.assertTrue(full.complete)
        self.assertFalse(full.partial_aggregates)
        full_paid = next(r for r in full.rows if r["status"] == "paid")

        partial = execute(plan, Budget(max_steps=6))
        self.assertFalse(partial.complete)
        self.assertEqual(partial.termination, TerminationReason.BUDGET_STEPS)
        self.assertTrue(partial.partial_aggregates)  # 明确标记：不是最终值
        partial_paid = next(r for r in partial.rows if r["status"] == "paid")
        self.assertLess(partial_paid["n"], full_paid["n"])

    def test_cancelled_before_start(self):
        budget = Budget()
        budget.cancel()
        result = execute(Scan(users), budget)
        self.assertFalse(result.complete)
        self.assertEqual(result.termination, TerminationReason.CANCELLED)
        self.assertEqual(result.rows, [])


class TestLimitVsBudget(unittest.TestCase):
    def test_limit_early_stop_is_normal(self):
        result = execute(Limit(Scan(make_bulk_table(10)), 3))
        self.assertTrue(result.complete)
        self.assertEqual(result.termination, TerminationReason.LIMIT)
        self.assertEqual(len(result.rows), 3)

    def test_limit_exactly_input_size_is_exhausted(self):
        result = execute(Limit(Scan(make_bulk_table(10)), 10))
        self.assertEqual(result.termination, TerminationReason.EXHAUSTED)

    def test_limit_above_input_size_is_exhausted(self):
        result = execute(Limit(Scan(make_bulk_table(10)), 99))
        self.assertEqual(result.termination, TerminationReason.EXHAUSTED)
        self.assertEqual(len(result.rows), 10)

    def test_limit_distinct_from_budget_interruption(self):
        limited = execute(Limit(Scan(make_bulk_table(10)), 3), Budget(max_steps=100))
        self.assertEqual(limited.termination, TerminationReason.LIMIT)
        self.assertTrue(limited.complete)
        interrupted = execute(Limit(Scan(make_bulk_table(10)), 3), Budget(max_steps=2))
        self.assertEqual(interrupted.termination, TerminationReason.BUDGET_STEPS)
        self.assertFalse(interrupted.complete)


class TestSharedBudgetAcrossRepeatedSubplans(unittest.TestCase):
    def setUp(self):
        self.a = Table("a", ("x",), ((1,), (2,), (3,)))
        self.b = Table("b", ("y",), ((10,), (20,), (30,), (40,)))

    def test_repeated_inner_subplan_fully_accounted(self):
        # 内层 Scan 重复执行 3 次：rows = 3(外Scan)+3(外循环)+12(内Scan)+12(内循环)
        plan = NestedLoopJoin(Scan(self.a), Scan(self.b), lambda l, r: True)
        result = execute(plan)
        self.assertTrue(result.complete)
        self.assertEqual(len(result.rows), 12)
        self.assertEqual(result.stats.rows_processed, 30)
        self.assertEqual(result.stats.steps, 30)

    def test_budget_interrupts_repeated_subplan_without_losing_count(self):
        plan = NestedLoopJoin(Scan(self.a), Scan(self.b), lambda l, r: True)
        result = execute(plan, Budget(max_steps=10))
        self.assertFalse(result.complete)
        self.assertEqual(result.termination, TerminationReason.BUDGET_STEPS)
        self.assertEqual(result.stats.steps, 11)  # 越界一步也记账，不漏记
        self.assertLess(len(result.rows), 12)


class TestTempResourcesAndCancel(unittest.TestCase):
    def test_sort_spills_and_releases_temp_files(self):
        registry = ResourceRegistry()
        table = make_bulk_table(257)
        result = execute(Sort(Scan(table), "v", spill_bytes=1024), registry=registry)
        self.assertTrue(result.complete)
        self.assertEqual(len(result.rows), 257)
        self.assertEqual([r["v"] for r in result.rows],
                         sorted((i * 91) % 1000 for i in range(257)))
        self.assertGreater(registry.temp_files_created, 0)   # 确实落盘过
        self.assertEqual(registry.active_temp_files(), [])   # 结束后全部释放

    def test_sort_reverse(self):
        result = execute(Sort(Scan(make_bulk_table(50)), "v", reverse=True))
        values = [r["v"] for r in result.rows]
        self.assertEqual(values, sorted(values, reverse=True))

    def test_cancel_releases_temp_files(self):
        registry = ResourceRegistry()
        budget = Budget(cancel_after_steps=60)
        result = execute(
            Sort(Scan(make_bulk_table(300)), "v", spill_bytes=1024),
            budget,
            registry,
        )
        self.assertFalse(result.complete)
        self.assertEqual(result.termination, TerminationReason.CANCELLED)
        self.assertGreater(registry.temp_files_created, 0)   # 取消前已落盘
        self.assertEqual(registry.active_temp_files(), [])   # 取消后全部释放

    def test_budget_interrupt_releases_temp_files(self):
        registry = ResourceRegistry()
        result = execute(
            Sort(Scan(make_bulk_table(300)), "v", spill_bytes=1024),
            Budget(max_steps=80),
            registry=registry,
        )
        self.assertEqual(result.termination, TerminationReason.BUDGET_STEPS)
        self.assertGreater(registry.temp_files_created, 0)
        self.assertEqual(registry.active_temp_files(), [])


class TestNullSemantics(unittest.TestCase):
    def test_filter_three_valued_logic(self):
        result = execute(Filter(Scan(users), lambda r: sql_gt(r["age"], 30)))
        names = sorted(r["name"] for r in result.rows)
        self.assertEqual(names, ["alice", "dave", "frank"])  # NULL age 被过滤

    def test_join_null_key_never_matches(self):
        plan = HashJoin(Scan(users), Scan(orders), "user_id", "user_id")
        result = execute(plan)
        order_ids = {r["order_id"] for r in result.rows}
        self.assertNotIn(106, order_ids)  # user_id=NULL 的订单不匹配
        self.assertNotIn(107, order_ids)  # user_id=9 无对应用户

    def test_aggregate_ignores_nulls(self):
        plan = HashAggregate(
            Scan(orders),
            group_by=[],
            aggregates=[
                ("count_star", None, "n_all"),
                ("count", "amount", "n_amount"),
                ("sum", "amount", "total"),
                ("avg", "amount", "avg_amount"),
                ("min", "amount", "min_amount"),
                ("max", "amount", "max_amount"),
            ],
        )
        (row,) = execute(plan).rows
        self.assertEqual(row["n_all"], 12)     # count(*) 含 NULL 行
        self.assertEqual(row["n_amount"], 10)  # count(col) 忽略 NULL
        self.assertEqual(row["total"], 888.5)
        self.assertAlmostEqual(row["avg_amount"], 88.85)
        self.assertEqual(row["min_amount"], 15.0)
        self.assertEqual(row["max_amount"], 300.0)

    def test_all_null_group_aggregates(self):
        plan = HashAggregate(
            Scan(orders),
            group_by=["status"],
            aggregates=[("count_star", None, "n"), ("sum", "amount", "total")],
        )
        rows = {r["status"]: r for r in execute(plan).rows}
        self.assertEqual(rows["cancelled"]["n"], 1)
        self.assertIsNone(rows["cancelled"]["total"])  # 全 NULL 组 sum 为 NULL

    def test_group_by_nulls_group_together(self):
        plan = HashAggregate(
            Scan(users), group_by=["city"], aggregates=[("count_star", None, "n")]
        )
        rows = {r["city"]: r["n"] for r in execute(plan).rows}
        self.assertEqual(rows[None], 2)        # dave + grace 归入 NULL 组
        self.assertEqual(rows["beijing"], 3)   # 含重复行
        self.assertEqual(rows["shanghai"], 2)
        self.assertEqual(rows["shenzhen"], 1)


class TestMultisetSemantics(unittest.TestCase):
    def test_duplicates_preserved_through_filter_project(self):
        plan = Project(
            Filter(Scan(users), lambda r: sql_eq(r["city"], "beijing")),
            [("name", "name")],
        )
        result = execute(plan)
        self.assertEqual([r["name"] for r in result.rows], ["bob", "erin", "erin"])

    def test_count_star_counts_duplicates(self):
        plan = HashAggregate(Scan(users), [], [("count_star", None, "n")])
        (row,) = execute(plan).rows
        self.assertEqual(row["n"], 8)  # 7 个逻辑用户 + 1 个重复行

    def test_join_multiplies_duplicates(self):
        # user_id=5 在 users 中出现 2 次，订单 108/109 各匹配 2 行
        plan = HashJoin(Scan(users), Scan(orders), "user_id", "user_id")
        result = execute(plan)
        u5 = [r for r in result.rows if r["user_id"] == 5]
        self.assertEqual(len(u5), 4)


if __name__ == "__main__":
    unittest.main()
