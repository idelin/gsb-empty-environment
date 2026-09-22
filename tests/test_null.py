import unittest

from pullpipe.null import (
    Null,
    and3,
    arith3,
    cmp3,
    is_distinct,
    is_false,
    is_null,
    is_true,
    not3,
    or3,
)


class TestNull(unittest.TestCase):
    def test_singleton_and_repr(self):
        self.assertIs(Null, Null)
        self.assertEqual(repr(Null), "NULL")
        self.assertTrue(is_null(Null))
        self.assertEqual(hash(Null), 0x5E5E5E)

    def test_bool_forbidden(self):
        with self.assertRaises(TypeError):
            bool(Null)

    def test_comparison_propagates_null(self):
        for op in ("=", "<>", "<", "<=", ">", ">="):
            self.assertIs(cmp3(Null, op, 1), Null)
            self.assertIs(cmp3(1, op, Null), Null)
            self.assertIs(cmp3(Null, op, Null), Null)
        self.assertTrue(cmp3(1, "=", 1))
        self.assertFalse(cmp3(1, "=", 2))
        self.assertTrue(cmp3(1, "<>", 2))

    def test_truth_tables(self):
        vals = (True, False, Null)
        # NOT
        self.assertIs(not3(True), False)
        self.assertIs(not3(False), True)
        self.assertIs(not3(Null), Null)
        # AND: False AND x = False；True AND Null = Null
        for a in vals:
            self.assertIs(and3(a, False), False)
            self.assertIs(and3(False, a), False)
        self.assertIs(and3(True, Null), Null)
        self.assertIs(and3(Null, True), Null)
        self.assertIs(and3(Null, Null), Null)
        self.assertIs(and3(True, True), True)
        # OR: True OR x = True；False OR Null = Null
        for a in vals:
            self.assertIs(or3(a, True), True)
            self.assertIs(or3(True, a), True)
        self.assertIs(or3(False, Null), Null)
        self.assertIs(or3(Null, False), Null)
        self.assertIs(or3(Null, Null), Null)
        self.assertIs(or3(False, False), False)

    def test_where_predicate_rule(self):
        self.assertTrue(is_true(True))
        self.assertFalse(is_true(False))
        self.assertFalse(is_true(Null))  # Unknown 不通过 WHERE
        self.assertTrue(is_false(False))
        self.assertFalse(is_false(Null))

    def test_is_distinct_from(self):
        self.assertFalse(is_distinct(Null, Null))  # NULL 被当作已知值
        self.assertTrue(is_distinct(Null, 1))
        self.assertTrue(is_distinct(1, Null))
        self.assertFalse(is_distinct(1, 1))
        self.assertTrue(is_distinct(1, 2))

    def test_arith_null_propagation(self):
        for op in ("+", "-", "*", "/"):
            self.assertIs(arith3(Null, op, 2), Null)
            self.assertIs(arith3(2, op, Null), Null)
        self.assertEqual(arith3(6, "/", 3), 2)
        with self.assertRaises(ZeroDivisionError):
            arith3(1, "/", 0)

    def test_null_groups_together_via_hash_identity(self):
        # 字典语义：Null 单例可作为分组键，所有 NULL 归一组（GROUP BY 语义）
        buckets = {Null: []}
        buckets[Null].append(1)
        buckets[Null].append(2)
        self.assertEqual(buckets[Null], [1, 2])
        # 但等值比较仍是 Unknown
        self.assertIs(Null == Null, Null)


if __name__ == "__main__":
    unittest.main()
