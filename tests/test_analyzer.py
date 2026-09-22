import unittest

from minisql import (And, Compare, EnumScheme, Interval, IsNull, Not, Or,
                     Param, PartitionedTable, RangeScheme, analyze)


def make_range_table():
    scheme = RangeScheme([
        ("p0", Interval.of(None, True, 0, False)),    # (-inf, 0)
        ("p1", Interval.of(0, True, 100, False)),     # [0, 100)
        ("p2", Interval.of(100, True, None, True)),   # [100, +inf)
    ])
    return PartitionedTable("t", ["id", "k"], "k", scheme)


def kept_of(decisions):
    return {d.partition for d in decisions if d.kept}


class TestRangePruning(unittest.TestCase):
    def setUp(self):
        self.table = make_range_table()

    def test_point_lookup_keeps_one_partition(self):
        d = analyze(Compare("k", "=", 50), self.table)
        self.assertEqual(kept_of(d), {"p1"})

    def test_boundary_value_goes_to_correct_partition(self):
        # 100 属于 [100,+inf)，开闭边界不漏不重
        d = analyze(Compare("k", "=", 100), self.table)
        self.assertEqual(kept_of(d), {"p2"})
        d = analyze(Compare("k", "<", 100), self.table)
        self.assertEqual(kept_of(d), {"p0", "p1"})
        d = analyze(Compare("k", ">=", 100), self.table)
        self.assertEqual(kept_of(d), {"p2"})

    def test_and_narrows_to_single_partition(self):
        d = analyze(And((Compare("k", ">=", 0), Compare("k", "<", 100))), self.table)
        self.assertEqual(kept_of(d), {"p1"})

    def test_or_unions_across_partitions(self):
        pred = Or((Compare("k", "<", 0), Compare("k", ">=", 100)))
        d = analyze(pred, self.table)
        self.assertEqual(kept_of(d), {"p0", "p2"})

    def test_not_is_flipped(self):
        d = analyze(Not(Compare("k", "<", 100)), self.table)
        self.assertEqual(kept_of(d), {"p2"})

    def test_not_equal_single_point_partition(self):
        scheme = RangeScheme([
            ("rest_lo", Interval.of(None, True, 5, False)),
            ("only5", Interval.of(5, True, 5, True)),
            ("rest_hi", Interval.of(5, False, None, True)),
        ])
        table = PartitionedTable("t", ["id", "k"], "k", scheme)
        d = analyze(Compare("k", "!=", 5), table)
        self.assertEqual(kept_of(d), {"rest_lo", "rest_hi"})

    def test_skip_decisions_carry_proof(self):
        d = analyze(Compare("k", "=", 50), self.table)
        skipped = {x.partition: x for x in d if not x.kept}
        self.assertEqual(set(skipped), {"p0", "p2", "p_null"})
        # 范围分区：区间不相交 ⇒ 每行均为 FALSE
        for name in ("p0", "p2"):
            self.assertIn("不相交", skipped[name].proof)
            self.assertIn("FALSE", skipped[name].proof)
        # NULL 分区：三值逻辑 UNKNOWN ⇒ WHERE 不保留
        self.assertIn("UNKNOWN", skipped["p_null"].proof)


class TestNullPartition(unittest.TestCase):
    def setUp(self):
        self.table = make_range_table()

    def test_is_null_selects_only_null_partition(self):
        d = analyze(IsNull("k"), self.table)
        self.assertEqual(kept_of(d), {"p_null"})

    def test_is_not_null_prunes_null_partition(self):
        d = analyze(IsNull("k", negated=True), self.table)
        self.assertEqual(kept_of(d), {"p0", "p1", "p2"})

    def test_equality_prunes_null_partition_with_3vl_proof(self):
        d = analyze(Compare("k", "=", 50), self.table)
        null_dec = next(x for x in d if x.partition == "p_null")
        self.assertFalse(null_dec.kept)
        self.assertIn("UNKNOWN", null_dec.proof)

    def test_compare_with_null_literal_prunes_everything(self):
        d = analyze(Compare("k", "=", None), self.table)
        self.assertEqual(kept_of(d), set())


class TestUnboundParam(unittest.TestCase):
    def setUp(self):
        self.table = make_range_table()

    def test_unbound_param_keeps_all_range_partitions(self):
        d = analyze(Compare("k", "=", Param("1")), self.table, params=None)
        self.assertEqual(kept_of(d), {"p0", "p1", "p2"})  # 保守保留
        reasons = " ".join(x.proof for x in d if x.kept)
        self.assertIn("未绑定", reasons)

    def test_unbound_param_still_prunes_null_partition(self):
        # NULL = $1 恒为 UNKNOWN，与参数取值无关，可证明安全跳过
        d = analyze(Compare("k", "=", Param("1")), self.table, params=None)
        null_dec = next(x for x in d if x.partition == "p_null")
        self.assertFalse(null_dec.kept)

    def test_bound_param_prunes_precisely(self):
        d = analyze(Compare("k", "=", Param("1")), self.table, params={"1": -7})
        self.assertEqual(kept_of(d), {"p0"})


class TestEnumPruning(unittest.TestCase):
    def setUp(self):
        scheme = EnumScheme(
            {"p_std": {"bronze", "silver"}, "p_prem": {"gold"}},
            catch_all="p_other",
        )
        self.table = PartitionedTable("u", ["id", "level"], "level", scheme)

    def test_eq_selects_single_enum_partition(self):
        d = analyze(Compare("level", "=", "gold"), self.table)
        self.assertEqual(kept_of(d), {"p_prem"})

    def test_or_of_equals_unions_enum_partitions(self):
        pred = Or((Compare("level", "=", "bronze"), Compare("level", "=", "gold")))
        d = analyze(pred, self.table)
        self.assertEqual(kept_of(d), {"p_std", "p_prem"})

    def test_ne_prunes_single_value_partition(self):
        d = analyze(Compare("level", "!=", "gold"), self.table)
        self.assertNotIn("p_prem", kept_of(d))
        self.assertIn("p_std", kept_of(d))

    def test_catch_all_is_conservatively_kept(self):
        d = analyze(Compare("level", "=", "diamond"), self.table)
        self.assertEqual(kept_of(d), {"p_other"})

    def test_unlisted_value_without_catch_all_raises_on_insert(self):
        scheme = EnumScheme({"p": {"a"}})
        table = PartitionedTable("u", ["id", "level"], "level", scheme)
        with self.assertRaises(ValueError):
            table.insert({"id": 1, "level": "zzz"})


class TestOtherColumns(unittest.TestCase):
    def test_predicate_on_non_key_column_keeps_everything(self):
        table = make_range_table()
        d = analyze(Compare("id", "=", 1), table)
        self.assertEqual(kept_of(d), {"p0", "p1", "p2", "p_null"})

    def test_mixed_or_keeps_null_partition_via_non_key_branch(self):
        table = make_range_table()
        pred = Or((Compare("k", "=", 50), Compare("id", "=", 9)))
        d = analyze(pred, table)
        self.assertIn("p_null", kept_of(d))


if __name__ == "__main__":
    unittest.main()
