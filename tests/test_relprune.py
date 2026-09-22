"""列裁剪与本地执行的单元测试（Python 3.11 标准库 unittest）。"""
import unittest

from data import USERS, USERS_SCHEMA, ORDERS, ORDERS_SCHEMA
from exprs import BinOp, Col, IsNull, Lit
from plan import Agg, Aggregate, Filter, Join, Project, Scan, Sort, prune


def users_scan():
    return Scan("users", USERS_SCHEMA, USERS)


def orders_scan():
    return Scan("orders", ORDERS_SCHEMA, ORDERS)


def evidence_for(root, needed, operator_prefix):
    _, evs = prune(root, needed)
    return next(e for e in evs if e.operator.startswith(operator_prefix))


class TestCountStarZeroColumns(unittest.TestCase):
    """COUNT(*) 不能因子算子无列而变空表。"""

    def test_count_star_with_zero_column_scan(self):
        root = Aggregate((), (Agg("count_star", None, "cnt"),), users_scan())
        pruned, evs = prune(root, frozenset({"cnt"}))
        scan_ev = evidence_for(root, frozenset({"cnt"}), "Scan")
        self.assertEqual(scan_ev.kept, ())  # 全部列被裁掉
        self.assertEqual(set(scan_ev.pruned), set(USERS_SCHEMA))
        cols, rows = pruned.run()
        self.assertEqual(cols, ["cnt"])
        self.assertEqual(rows, [{"cnt": len(USERS)}])  # 6 行，不是空表

    def test_zero_column_scan_preserves_multiplicity(self):
        scan = Scan("users", USERS_SCHEMA, USERS, kept=())
        cols, rows = scan.run()
        self.assertEqual(cols, [])
        self.assertEqual(len(rows), len(USERS))
        self.assertTrue(all(r == {} for r in rows))


class TestSortColumnNotInOutput(unittest.TestCase):
    """输出不含排序列，但执行须保留到排序之后。"""

    def build(self):
        return Project(
            (("name", Col("users.name")),),
            Sort(((Col("users.city"), False),), users_scan()),
        )

    def test_sort_key_survives_until_sort_then_dropped(self):
        pruned, evs = prune(self.build(), frozenset({"name"}))
        scan_ev = next(e for e in evs if e.operator.startswith("Scan"))
        self.assertIn("users.city", scan_ev.kept)  # 排序键保留到排序
        sort_ev = next(e for e in evs if e.operator.startswith("Sort"))
        self.assertIn("users.city", sort_ev.pruned)  # 排序后裁掉
        cols, rows = pruned.run()
        self.assertEqual(cols, ["name"])  # 输出不含排序列

    def test_sort_order_and_null_placement(self):
        pruned, _ = prune(self.build(), frozenset({"name"}))
        _, rows = pruned.run()
        names = [r["name"] for r in rows]
        # 升序 NULLS LAST：Lyon, Lyon, Nice, Paris, Paris, NULL(Ben)
        self.assertEqual(names, ["Dee", "Dee", "Eli", "Ada", "Cid", "Ben"])

    def test_desc_nulls_first(self):
        root = Project(
            (("name", Col("users.name")),),
            Sort(((Col("users.city"), True),), users_scan()),
        )
        pruned, _ = prune(root, frozenset({"name"}))
        _, rows = pruned.run()
        self.assertEqual(rows[0]["name"], "Ben")  # 降序 NULLS FIRST


class TestSameNameColumns(unittest.TestCase):
    """同名列按来源（限定名）区分。"""

    def test_qualified_names_distinguish_sources(self):
        root = Join(
            BinOp("=", Col("users.id"), Col("orders.user_id")),
            users_scan(), orders_scan(),
        )
        needed = frozenset({"users.id", "orders.id"})
        pruned, evs = prune(root, needed)
        scan_u = next(e for e in evs if e.operator == "Scan(users)")
        scan_o = next(e for e in evs if e.operator == "Scan(orders)")
        self.assertIn("users.id", scan_u.kept)
        self.assertIn("orders.id", scan_o.kept)
        self.assertNotIn("orders.id", scan_u.kept)
        cols, rows = pruned.run()
        # 同名列按来源区分：users.id 与 orders.id 各自独立；连接条件列 orders.user_id 也保留
        self.assertEqual(cols, ["users.id", "orders.id", "orders.user_id"])
        pairs = {(r["users.id"], r["orders.id"]) for r in rows}
        self.assertIn((1, 10), pairs)
        self.assertIn((3, 13), pairs)


class TestNullSemantics(unittest.TestCase):
    """NULL 三值逻辑：过滤 UNKNOWN 不保留；连接 NULL 不匹配。"""

    def test_filter_excludes_unknown(self):
        root = Filter(BinOp(">=", Col("users.age"), Lit(18)), users_scan())
        pruned, _ = prune(root, frozenset({"users.name"}))
        _, rows = pruned.run()
        names = {r["users.name"] for r in rows}
        self.assertNotIn("Eli", names)  # age 为 NULL -> UNKNOWN -> 过滤掉
        self.assertNotIn("Ben", names)  # 17 < 18

    def test_join_null_key_never_matches(self):
        root = Join(
            BinOp("=", Col("users.id"), Col("orders.user_id")),
            users_scan(), orders_scan(),
        )
        pruned, _ = prune(root, frozenset({"orders.id"}))
        _, rows = pruned.run()
        order_ids = {r["orders.id"] for r in rows}
        self.assertNotIn(14, order_ids)  # user_id 为 NULL 的订单不匹配任何用户

    def test_is_null_and_not_unknown_propagation(self):
        root = Filter(IsNull(Col("users.city")), users_scan())
        pruned, _ = prune(root, frozenset({"users.name"}))
        _, rows = pruned.run()
        self.assertEqual([r["users.name"] for r in rows], ["Ben"])


class TestMultiplicity(unittest.TestCase):
    """多重集合语义：裁剪与投影不去重。"""

    def test_projection_keeps_duplicate_rows(self):
        root = Project((("city", Col("users.city")),), users_scan())
        pruned, _ = prune(root, frozenset({"city"}))
        _, rows = pruned.run()
        self.assertEqual(len(rows), len(USERS))
        lyon = [r for r in rows if r["city"] == "Lyon"]
        self.assertEqual(len(lyon), 2)  # 重复行保留

    def test_join_duplicate_rows_preserved(self):
        root = Join(
            BinOp("=", Col("users.id"), Col("orders.user_id")),
            users_scan(), orders_scan(),
        )
        pruned, _ = prune(root, frozenset({"orders.id"}))
        _, rows = pruned.run()
        self.assertEqual(sum(1 for r in rows if r["orders.id"] == 13), 2)


class TestExpressionImplicitDeps(unittest.TestCase):
    """表达式隐式依赖：不出现在输出中的列也必须保留。"""

    def test_filter_and_join_deps_kept(self):
        root = Project(
            (("name", Col("users.name")),),
            Filter(
                BinOp("=", Col("orders.status"), Lit("paid")),
                Join(BinOp("=", Col("users.id"), Col("orders.user_id")),
                     users_scan(), orders_scan()),
            ),
        )
        pruned, evs = prune(root, frozenset({"name"}))
        scan_o = next(e for e in evs if e.operator == "Scan(orders)")
        self.assertIn("orders.status", scan_o.kept)    # 过滤隐式依赖
        self.assertIn("orders.user_id", scan_o.kept)   # 连接隐式依赖
        self.assertIn("orders.note", scan_o.pruned)    # 无引用则裁掉
        cols, rows = pruned.run()
        self.assertEqual(cols, ["name"])
        self.assertEqual(len(rows), 4)  # paid 订单 4 行：10/11/13/13；订单 14 的 user_id 为 NULL 不匹配


class TestAggregateSemantics(unittest.TestCase):
    """聚合的 NULL 与分组语义。"""

    def test_group_by_null_groups_together(self):
        root = Aggregate(
            (("city", Col("users.city")),),
            (Agg("count_star", None, "n"), Agg("count", Col("users.age"), "aged")),
            users_scan(),
        )
        pruned, _ = prune(root, frozenset({"city", "n", "aged"}))
        _, rows = pruned.run()
        by_city = {r["city"]: r for r in rows}
        self.assertEqual(by_city[None]["n"], 1)       # NULL 键成组
        self.assertEqual(by_city["Nice"]["aged"], 0)  # COUNT(age) 忽略 NULL
        self.assertEqual(by_city["Lyon"]["n"], 2)     # 重复行计入

    def test_sum_avg_ignore_nulls(self):
        root = Aggregate(
            (),
            (Agg("sum", Col("orders.amount"), "s"),
             Agg("avg", Col("orders.amount"), "a")),
            orders_scan(),
        )
        pruned, _ = prune(root, frozenset({"s", "a"}))
        _, rows = pruned.run()
        amounts = [120.0, 45.0, 45.0, 45.0, 60.0]  # None 被忽略
        self.assertEqual(rows[0]["s"], sum(amounts))
        self.assertAlmostEqual(rows[0]["a"], sum(amounts) / len(amounts))


class TestPruningEvidence(unittest.TestCase):
    """裁剪证据完整、可追溯。"""

    def test_evidence_covers_all_operators(self):
        root = Project(
            (("name", Col("users.name")),),
            Sort(((Col("users.city"), False),),
                 Filter(BinOp(">=", Col("users.age"), Lit(18)), users_scan())),
        )
        _, evs = prune(root, frozenset({"name"}))
        ops = [e.operator for e in evs]
        self.assertEqual(ops, ["Scan(users)", "Filter", "Sort", "Project"])
        for ev in evs:
            self.assertTrue(ev.reason)
        scan_ev = evs[0]
        self.assertEqual(set(scan_ev.kept) | set(scan_ev.pruned), set(USERS_SCHEMA))
        self.assertIn("users.email", scan_ev.pruned)


if __name__ == "__main__":
    unittest.main()
