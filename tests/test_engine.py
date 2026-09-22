import random
import unittest

from minisql import (And, Compare, EnumScheme, Interval, IsNull, Not, Or,
                     Param, PartitionedTable, RangeScheme, analyze, select,
                     select_full_scan)


def make_orders():
    scheme = RangeScheme([
        ("p_low", Interval.of(None, True, 0, False)),      # (-inf, 0)
        ("p_mid", Interval.of(0, True, 100, False)),       # [0, 100)
        ("p_high", Interval.of(100, True, None, True)),    # [100, +inf)
    ])
    return PartitionedTable("orders", ["id", "amount", "tag"], "amount", scheme)


def load(table, rows):
    for row in rows:
        table.insert(row)
    return table


class TestBoundaryRows(unittest.TestCase):
    """开闭边界上的行必须恰好落在一个分区，且查询不漏一行。"""

    def setUp(self):
        self.table = load(make_orders(), [
            {"id": 1, "amount": -1, "tag": "a"},
            {"id": 2, "amount": 0, "tag": "b"},     # p_mid 下界（闭）
            {"id": 3, "amount": 99, "tag": "c"},
            {"id": 4, "amount": 100, "tag": "d"},   # p_high 下界（闭）
            {"id": 5, "amount": 10**6, "tag": "e"},
        ])

    def test_rows_routed_to_exactly_one_partition(self):
        sizes = {p.name: len(p.rows) for p in self.table.partitions}
        self.assertEqual(sizes["p_low"], 1)
        self.assertEqual(sizes["p_mid"], 2)   # 0 和 99
        self.assertEqual(sizes["p_high"], 2)  # 100 和 10^6
        self.assertEqual(sum(sizes.values()), 5)

    def test_lower_bound_inclusive(self):
        res = select(self.table, Compare("amount", ">=", 0))
        self.assertEqual(sorted(r["id"] for r in res.rows), [2, 3, 4, 5])
        self.assertEqual(res.scanned, ["p_mid", "p_high"])

    def test_upper_bound_exclusive(self):
        res = select(self.table, Compare("amount", "<", 100))
        self.assertEqual(sorted(r["id"] for r in res.rows), [1, 2, 3])
        self.assertEqual(res.scanned, ["p_low", "p_mid"])

    def test_open_closed_scheme(self):
        # 显式混合开闭：[0,10]、(10,20] —— 10 属于前者，11 属于后者
        # 覆盖性校验要求覆盖整条整数线，用无界区间补齐两端
        scheme = RangeScheme([
            ("neg", Interval.of(None, True, 0, False)),
            ("a", Interval.of(0, True, 10, True)),
            ("b", Interval.of(10, False, 20, True)),
            ("pos", Interval.of(20, False, None, True)),
        ])
        table = PartitionedTable("t", ["id", "k"], "k", scheme)
        load(table, [{"id": 1, "k": 10}, {"id": 2, "k": 11}, {"id": 3, "k": 20}])
        self.assertEqual(table.partitions[1].rows[0]["id"], 1)   # 10 -> a
        self.assertEqual(table.partitions[2].rows[0]["id"], 2)   # 11 -> b
        res = select(table, Compare("k", ">", 10))
        self.assertEqual(sorted(r["id"] for r in res.rows), [2, 3])
        self.assertNotIn("a", res.scanned)


class TestOrUnionAndMultiset(unittest.TestCase):
    def setUp(self):
        # 含完全重复的行：多重集合语义必须保留重复
        self.table = load(make_orders(), [
            {"id": 1, "amount": -5, "tag": "x"},
            {"id": 2, "amount": -5, "tag": "x"},
            {"id": 3, "amount": -5, "tag": "x"},
            {"id": 4, "amount": 50, "tag": "y"},
            {"id": 5, "amount": 150, "tag": "z"},
            {"id": 6, "amount": 150, "tag": "z"},
        ])

    def test_or_across_partitions_is_union(self):
        pred = Or((Compare("amount", "<", 0), Compare("amount", ">=", 100)))
        res = select(self.table, pred)
        self.assertEqual(res.scanned, ["p_low", "p_high"])
        # 3 行来自 p_low，2 行来自 p_high，并集不去重
        self.assertEqual(len(res.rows), 5)
        self.assertEqual([r["id"] for r in res.rows], [1, 2, 3, 5, 6])

    def test_duplicates_preserved_by_point_query(self):
        res = select(self.table, Compare("amount", "=", -5))
        self.assertEqual(len(res.rows), 3)
        self.assertEqual(res.scanned, ["p_low"])

    def test_count_star_matches_full_scan(self):
        pred = Not(Compare("amount", ">=", 0))
        res = select(self.table, pred)
        self.assertEqual(len(res.rows), 3)
        self.assertEqual(res.rows, select_full_scan(self.table, pred))


class TestNullSemantics(unittest.TestCase):
    def setUp(self):
        self.table = load(make_orders(), [
            {"id": 1, "amount": None, "tag": "n1"},
            {"id": 2, "amount": None, "tag": "n2"},
            {"id": 3, "amount": 7, "tag": "v"},
        ])

    def test_null_rows_routed_to_null_partition(self):
        null_part = self.table.partitions[-1]
        self.assertEqual(null_part.name, "p_null")
        self.assertEqual(len(null_part.rows), 2)

    def test_is_null_scans_only_null_partition(self):
        res = select(self.table, IsNull("amount"))
        self.assertEqual(res.scanned, ["p_null"])
        self.assertEqual([r["id"] for r in res.rows], [1, 2])

    def test_equality_skips_null_partition(self):
        res = select(self.table, Compare("amount", "=", 7))
        self.assertNotIn("p_null", res.scanned)
        self.assertEqual([r["id"] for r in res.rows], [3])

    def test_is_not_null_skips_null_partition(self):
        res = select(self.table, IsNull("amount", negated=True))
        self.assertNotIn("p_null", res.scanned)
        self.assertEqual([r["id"] for r in res.rows], [3])

    def test_unknown_rows_filtered_not_returned(self):
        # NULL = 7 是 UNKNOWN，WHERE 不保留
        res = select_full_scan(self.table, Compare("amount", "=", 7))
        self.assertEqual([r["id"] for r in res], [3])


class TestParams(unittest.TestCase):
    def setUp(self):
        self.table = load(make_orders(), [
            {"id": 1, "amount": -5, "tag": "a"},
            {"id": 2, "amount": 50, "tag": "b"},
            {"id": 3, "amount": 500, "tag": "c"},
            {"id": 4, "amount": None, "tag": "n"},
        ])

    def test_unbound_param_conservatively_keeps_range_partitions(self):
        # 预编译流程：分析阶段参数未绑定 -> 保守保留；执行阶段绑定参数
        pred = Compare("amount", "=", Param("1"))
        plan = analyze(pred, self.table, params=None)
        res = select(self.table, pred, params={"1": 50}, decisions=plan)
        self.assertEqual(res.scanned, ["p_low", "p_mid", "p_high"])
        self.assertNotIn("p_null", res.scanned)  # NULL 分区仍可证明跳过
        self.assertEqual([r["id"] for r in res.rows], [2])

    def test_bound_param_prunes(self):
        decisions = analyze(Compare("amount", "=", Param("1")), self.table,
                            params={"1": 50})
        kept = [d.partition for d in decisions if d.kept]
        self.assertEqual(kept, ["p_mid"])

    def test_param_bound_to_null_returns_nothing(self):
        res = select(self.table, Compare("amount", "=", Param("1")), params={"1": None})
        self.assertEqual(res.rows, [])
        self.assertEqual(res.scanned, [])

    def test_unbound_param_execution_raises(self):
        with self.assertRaises(ValueError):
            select(self.table, Compare("amount", "=", Param("1")))


class TestEnumEndToEnd(unittest.TestCase):
    def setUp(self):
        scheme = EnumScheme(
            {"p_std": {"bronze", "silver"}, "p_prem": {"gold"}},
            catch_all="p_other",
        )
        self.table = load(
            PartitionedTable("users", ["id", "level"], "level", scheme),
            [
                {"id": 1, "level": "bronze"},
                {"id": 2, "level": "gold"},
                {"id": 3, "level": "diamond"},  # catch-all
                {"id": 4, "level": None},
                {"id": 5, "level": "gold"},
            ],
        )

    def test_eq_prunes_to_single_partition(self):
        res = select(self.table, Compare("level", "=", "gold"))
        self.assertEqual(res.scanned, ["p_prem"])
        self.assertEqual([r["id"] for r in res.rows], [2, 5])  # 重复值保留

    def test_or_unions_enum_partitions(self):
        pred = Or((Compare("level", "=", "bronze"), Compare("level", "=", "diamond")))
        res = select(self.table, pred)
        self.assertEqual(res.scanned, ["p_std", "p_other"])
        self.assertEqual([r["id"] for r in res.rows], [1, 3])

    def test_is_null_on_enum_key(self):
        res = select(self.table, IsNull("level"))
        self.assertEqual(res.scanned, ["p_null"])
        self.assertEqual([r["id"] for r in res.rows], [4])


class TestRandomizedEquivalence(unittest.TestCase):
    """随机数据 + 随机谓词：裁剪扫描必须与全表扫描逐行一致（含顺序与重复）。"""

    def test_equivalence(self):
        rng = random.Random(20260922)
        table = make_orders()
        for i in range(300):
            amount = rng.choice([None] + [rng.randint(-20, 220) for _ in range(4)])
            table.insert({"id": i, "amount": amount, "tag": rng.choice("xyz")})

        def rand_pred(depth=0):
            if depth >= 2 or rng.random() < 0.45:
                col = rng.choice(["amount", "amount", "tag"])
                if col == "amount" and rng.random() < 0.2:
                    return IsNull("amount", negated=rng.random() < 0.5)
                if col == "tag":
                    return Compare("tag", rng.choice(["=", "!="]), rng.choice("xyz"))
                return Compare("amount", rng.choice(["=", "!=", "<", "<=", ">", ">="]),
                               rng.randint(-30, 230))
            comb = rng.choice([And, Or])
            node = comb((rand_pred(depth + 1), rand_pred(depth + 1)))
            return Not(node) if rng.random() < 0.2 else node

        for _ in range(200):
            pred = rand_pred()
            pruned = select(table, pred).rows
            full = select_full_scan(table, pred)
            self.assertEqual(pruned, full, f"结果不一致: {pred}")


if __name__ == "__main__":
    unittest.main()
