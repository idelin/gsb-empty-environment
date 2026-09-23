"""unittest 测试：TopK 安全性、稳定序、NULL/多重集合语义、下推前提。"""

import unittest
from functools import cmp_to_key

from engine import (Aggregate, Filter, Join, OrderLimit, Pred, Scan,
                    TopKMerge, _kway_merge, make_cmp, sql_and, sql_cmp,
                    sql_not, sql_or)
from planner import (check_fk, check_unique, push_aggregate, push_filter,
                     rewrite_topk)
from synth_data import (DEPARTMENT_COLS, DEPARTMENT_PARTS, DEPT_YEARLY_COLS,
                        DEPT_YEARLY_PARTS, EMPLOYEE_COLS, EMPLOYEE_PARTS)

KEYS_DESC = [("e_salary", True)]


def emp_scan():
    return Scan("employees", EMPLOYEE_COLS, EMPLOYEE_PARTS)


def dept_scan():
    return Scan("departments", DEPARTMENT_COLS, DEPARTMENT_PARTS)


def ids(rows):
    return [r["e_id"] for r in rows]


def data(rows, cols):
    """只看数据列，忽略内部稳定序标记 _seq。"""
    return [{c: r[c] for c in cols} for r in rows]


JOIN_COLS = EMPLOYEE_COLS + DEPARTMENT_COLS


class TestTopK(unittest.TestCase):
    def test_local_n_is_offset_plus_limit(self):
        _, why = rewrite_topk(OrderLimit(emp_scan(), KEYS_DESC, 5, 3))
        plan, _ = rewrite_topk(OrderLimit(emp_scan(), KEYS_DESC, 5, 3))
        self.assertIsInstance(plan, TopKMerge)
        self.assertEqual(plan.local_n, 8)  # offset=5 limit=3 局部不能只取 3
        self.assertIn("8", why)

    def test_topk_matches_full_sort(self):
        logical = OrderLimit(emp_scan(), KEYS_DESC, offset=5, limit=3)
        plan, _ = rewrite_topk(logical)
        self.assertEqual(ids(plan.execute()), ids(logical.execute()))
        self.assertEqual(ids(plan.execute()), [11, 2, 8])  # Kim, Ben, Han

    def test_naive_local_limit_only_is_wrong(self):
        # 反例：offset=5 limit=3 时局部只取 3 行会漏掉 Kim(84000)
        scan = emp_scan()
        cmp_key = cmp_to_key(make_cmp(KEYS_DESC))
        runs = [sorted(p, key=cmp_key)[:3] for p in scan.partitions()]
        naive = _kway_merge(runs, cmp_key)[5:8]
        reference = OrderLimit(emp_scan(), KEYS_DESC, 5, 3).execute()
        self.assertNotEqual(ids(naive), ids(reference))

    def test_ties_keep_original_order_across_partitions(self):
        # Ben(seq=1) 与 Han(seq=7) 同为 78000，稳定序要求 Ben 在前
        rows = OrderLimit(emp_scan(), KEYS_DESC, 0, None).execute()
        tie = [r["e_id"] for r in rows if r["e_salary"] == 78000]
        self.assertEqual(tie, [2, 8])
        # 并列对跨越分区时 TopKMerge 同样稳定
        plan, _ = rewrite_topk(OrderLimit(emp_scan(), KEYS_DESC, 5, 3))
        self.assertEqual(ids(plan.execute()), [11, 2, 8])

    def test_nulls_last(self):
        rows = OrderLimit(emp_scan(), KEYS_DESC, 0, None).execute()
        self.assertIsNone(rows[-1]["e_salary"])
        self.assertEqual(rows[-1]["e_id"], 4)
        rows_asc = OrderLimit(emp_scan(), [("e_salary", False)], 0, None).execute()
        self.assertIsNone(rows_asc[-1]["e_salary"])  # 两个方向都 NULLS LAST

    def test_rewrite_rejected_without_limit(self):
        node = OrderLimit(emp_scan(), KEYS_DESC, 5, None)
        plan, why = rewrite_topk(node)
        self.assertIs(plan, node)
        self.assertIn("保留原计划", why)


class TestNullAndBagSemantics(unittest.TestCase):
    def test_three_valued_logic(self):
        self.assertIsNone(sql_cmp(">", None, 1))
        self.assertIs(sql_and(True, None), None)
        self.assertIs(sql_and(False, None), False)
        self.assertIs(sql_or(False, None), None)
        self.assertIs(sql_or(True, None), True)
        self.assertIsNone(sql_not(None))

    def test_filter_drops_unknown(self):
        pred = Pred("sal>70000", ["e_salary"],
                    lambda r: sql_cmp(">", r["e_salary"], 70000))
        rows = Filter(emp_scan(), pred).execute()
        self.assertNotIn(4, ids(rows))  # Dee 工资 NULL -> UNKNOWN -> 被过滤
        self.assertEqual(len(rows), 9)

    def test_null_join_key_never_matches(self):
        inner = Join(emp_scan(), dept_scan(), "e_dept", "d_id", "inner")
        got = ids(inner.execute())
        self.assertNotIn(7, got)  # Gil 部门为 NULL，内连接被丢弃
        left = Join(emp_scan(), dept_scan(), "e_dept", "d_id", "left")
        rows = left.execute()
        gil = [r for r in rows if r["e_id"] == 7][0]
        self.assertIsNone(gil["d_name"])  # 外连接补 NULL

    def test_bag_semantics_join_multiplies(self):
        # dept_yearly 中 y_dept=10 出现两次 -> 部门 10 的员工各产生 2 行
        dup = Scan("dept_yearly", DEPT_YEARLY_COLS, DEPT_YEARLY_PARTS)
        rows = Join(emp_scan(), dup, "e_dept", "y_dept", "inner").execute()
        dept10 = [r for r in rows if r["e_dept"] == 10]
        self.assertEqual(len(dept10), 5 * 2)  # 5 名员工 x 2 个年度

    def test_bag_semantics_aggregate_keeps_duplicates(self):
        # 61000 出现两次（Fay、Gil），SUM 不去重；NULL 工资被忽略
        agg = Aggregate(emp_scan(), [], [("sum", "e_salary", "s"),
                                         ("count", "*", "c1"),
                                         ("count", "e_salary", "c2")])
        row = agg.execute()[0]
        self.assertEqual(row["s"], 90000 + 78000 + 72000 + 85000 + 61000
                         + 61000 + 78000 + 86000 + 94000 + 84000 + 88000)
        self.assertEqual(row["c1"], 12)  # COUNT(*) 计全部行
        self.assertEqual(row["c2"], 11)  # COUNT(col) 忽略 NULL

    def test_aggregate_null_group_and_empty(self):
        agg = Aggregate(emp_scan(), ["e_dept"], [("count", "*", "c")])
        groups = {r["e_dept"]: r["c"] for r in agg.execute()}
        self.assertEqual(groups[None], 1)  # NULL 聚成一组
        empty = Filter(emp_scan(), Pred("false", [], lambda r: False))
        row = Aggregate(empty, [], [("count", "*", "c"),
                                    ("sum", "e_salary", "s")]).execute()[0]
        self.assertEqual(row["c"], 0)
        self.assertIsNone(row["s"])  # 空集 SUM 为 NULL


class TestFilterPushdown(unittest.TestCase):
    def loj(self):
        return Join(emp_scan(), dept_scan(), "e_dept", "d_id", "left")

    def test_pushdown_on_preserved_side(self):
        pred = Pred("sal>70000", ["e_salary"],
                    lambda r: sql_cmp(">", r["e_salary"], 70000))
        orig = Filter(self.loj(), pred)
        plan, why = push_filter(orig)
        self.assertIsInstance(plan, Join)
        self.assertEqual(plan.how, "left")
        self.assertEqual(data(plan.execute(), JOIN_COLS),
                         data(orig.execute(), JOIN_COLS))

    def test_null_rejecting_predicate_pushed_and_loj_becomes_inner(self):
        pred = Pred("budget>100000", ["d_budget"],
                    lambda r: sql_cmp(">", r["d_budget"], 100000))
        orig = Filter(self.loj(), pred)
        plan, why = push_filter(orig)
        self.assertEqual(plan.how, "inner")  # 退化为内连接
        self.assertIsInstance(plan.right, Filter)
        self.assertEqual(data(plan.execute(), JOIN_COLS),
                         data(orig.execute(), JOIN_COLS))
        self.assertIn("NULL 拒绝", why)

    def test_non_null_rejecting_predicate_kept(self):
        pred = Pred("budget IS NULL", ["d_budget"],
                    lambda r: r["d_budget"] is None)
        orig = Filter(self.loj(), pred)
        plan, why = push_filter(orig)
        self.assertIs(plan, orig)  # 不安全 -> 保留原计划
        self.assertIn("保留原计划", why)
        # 若错误下推会丢行：原计划结果含 Gil(e_id=7) 的补 NULL 行
        self.assertIn(7, ids(orig.execute()))

    def test_two_sided_predicate_kept(self):
        pred = Pred("e_dept=d_id", ["e_dept", "d_id"],
                    lambda r: sql_cmp("=", r["e_dept"], r["d_id"]))
        orig = Filter(self.loj(), pred)
        plan, _ = push_filter(orig)
        self.assertIs(plan, orig)


class TestAggregatePushdown(unittest.TestCase):
    AGGS = [("count", "*", "cnt"), ("sum", "e_salary", "total")]
    OUT = ["e_dept", "cnt", "total"]

    def test_inner_join_with_unique_and_fk_pushed(self):
        join = Join(emp_scan(), dept_scan(), "e_dept", "d_id", "inner")
        orig = Aggregate(join, ["e_dept"], self.AGGS)
        plan, why = push_aggregate(orig)
        self.assertIsInstance(plan, Join)
        self.assertIsInstance(plan.left, Aggregate)
        self.assertEqual(data(plan.execute(), self.OUT),
                         data(orig.execute(), self.OUT))
        self.assertIn("前提已验证", why)

    def test_left_join_with_unique_pushed(self):
        join = Join(emp_scan(), dept_scan(), "e_dept", "d_id", "left")
        orig = Aggregate(join, ["e_dept"], self.AGGS)
        plan, _ = push_aggregate(orig)
        self.assertIsInstance(plan, Join)
        self.assertEqual(data(plan.execute(), self.OUT),
                         data(orig.execute(), self.OUT))

    def test_non_unique_key_kept(self):
        dup = Scan("dept_yearly", DEPT_YEARLY_COLS, DEPT_YEARLY_PARTS)
        join = Join(emp_scan(), dup, "e_dept", "y_dept", "inner")
        orig = Aggregate(join, ["e_dept"], self.AGGS)
        plan, why = push_aggregate(orig)
        self.assertIs(plan, orig)  # 不安全 -> 保留原计划
        self.assertIn("不唯一", why)

    def test_missing_fk_kept_for_inner_join(self):
        # 部门表缺少 30 号部门 -> 外键不成立 -> 内连接下推被拒绝
        broken = Scan("dept_broken", DEPARTMENT_COLS,
                      [[(10, "Eng", 500000), (20, "Ops", None)]])
        join = Join(emp_scan(), broken, "e_dept", "d_id", "inner")
        orig = Aggregate(join, ["e_dept"], self.AGGS)
        plan, why = push_aggregate(orig)
        self.assertIs(plan, orig)
        self.assertIn("外键", why)

    def test_join_key_not_in_group_cols_kept(self):
        join = Join(emp_scan(), dept_scan(), "e_dept", "d_id", "inner")
        orig = Aggregate(join, [], self.AGGS)  # 全表聚合，连接键不在分组列
        plan, why = push_aggregate(orig)
        self.assertIs(plan, orig)
        self.assertIn("回连", why)

    def test_constraint_checkers(self):
        depts = dept_scan().execute()
        self.assertTrue(check_unique(depts, "d_id"))
        dup = Scan("dept_yearly", DEPT_YEARLY_COLS, DEPT_YEARLY_PARTS).execute()
        self.assertFalse(check_unique(dup, "y_dept"))
        self.assertTrue(check_fk(emp_scan().execute(), "e_dept", depts, "d_id"))
        # 员工部门 30 在 dept_yearly 中不存在 -> 外键不成立
        self.assertFalse(check_fk(emp_scan().execute(), "e_dept", dup, "y_dept"))


if __name__ == "__main__":
    unittest.main()
