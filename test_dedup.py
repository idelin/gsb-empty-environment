import math
import unittest

from dedup import (
    DedupEngine,
    HashTable,
    RowTypeError,
    SchemaError,
)

SCHEMA = [("id", "int"), ("name", "str"), ("score", "float")]


def force_collision(key):
    return 0  # 强制所有键哈希相同


class KeepFirstTest(unittest.TestCase):
    def test_keep_first_basic(self):
        eng = DedupEngine(["id"], keep="first")
        eng.add_batch(SCHEMA, [
            (1, "a", 1.0),
            (2, "b", 2.0),
            (1, "a2", 3.0),
        ])
        self.assertEqual(eng.result(), [(1, "a", 1.0), (2, "b", 2.0)])

    def test_keep_first_across_batches(self):
        eng = DedupEngine(["id"], keep="first")
        eng.add_batch(SCHEMA, [(1, "a", 1.0)])
        eng.add_batch(SCHEMA, [(1, "a2", 9.9), (2, "b", 2.0)])
        self.assertEqual(eng.result(), [(1, "a", 1.0), (2, "b", 2.0)])

    def test_result_order_by_kept_row_seq(self):
        # 保留行的原序号决定输出顺序，而不是键的顺序
        eng = DedupEngine(["id"], keep="first")
        eng.add_batch(SCHEMA, [(5, "e", 0.5), (3, "c", 0.3), (5, "e2", 0.55)])
        self.assertEqual(eng.result(), [(5, "e", 0.5), (3, "c", 0.3)])


class KeepLastTest(unittest.TestCase):
    def test_keep_last_basic(self):
        eng = DedupEngine(["id"], keep="last")
        eng.add_batch(SCHEMA, [
            (1, "a", 1.0),
            (2, "b", 2.0),
            (1, "a2", 3.0),
        ])
        # id=1 的保留行是序号为 2 的 (1, "a2", 3.0)，故排在 id=2 之后
        self.assertEqual(eng.result(), [(2, "b", 2.0), (1, "a2", 3.0)])

    def test_keep_last_batch_update_event_order(self):
        # 批次更新：旧 ID 的更新按声明事件序（批次序 + 批内行序）生效
        eng = DedupEngine(["id"], keep="last")
        eng.add_batch(SCHEMA, [(1, "v1", 1.0), (2, "w1", 2.0)])
        eng.add_batch(SCHEMA, [(1, "v2", 1.1)])
        eng.add_batch(SCHEMA, [(1, "v3", 1.2), (2, "w2", 2.1)])
        entries = eng.result_entries()
        self.assertEqual([row for _, row in entries],
                         [(1, "v3", 1.2), (2, "w2", 2.1)])
        self.assertEqual([seq for seq, _ in entries], [3, 4])

    def test_keep_last_result_order_uses_updating_row_seq(self):
        eng = DedupEngine(["id"], keep="last")
        eng.add_batch(SCHEMA, [(1, "a", 1.0)])
        eng.add_batch(SCHEMA, [(2, "b", 2.0)])
        eng.add_batch(SCHEMA, [(1, "a2", 3.0)])  # 更新旧 id=1
        self.assertEqual(eng.result(), [(2, "b", 2.0), (1, "a2", 3.0)])


class NullAndNaNTest(unittest.TestCase):
    def test_null_keys_are_equal(self):
        schema = [("g", "str"), ("v", "int")]
        eng = DedupEngine(["g"], keep="first")
        eng.add_batch(schema, [(None, 1), (None, 2), ("x", 3)])
        self.assertEqual(eng.result(), [(None, 1), ("x", 3)])

    def test_null_differs_from_nan(self):
        schema = [("g", "float"), ("v", "int")]
        eng = DedupEngine(["g"], keep="first")
        eng.add_batch(schema, [(None, 1), (math.nan, 2), (None, 3), (math.nan, 4)])
        # null 与 NaN 不同 -> 两组；各自组内去重
        self.assertEqual(eng.result(), [(None, 1), (math.nan, 2)])

    def test_nan_equals_nan(self):
        schema = [("g", "float"), ("v", "int")]
        eng = DedupEngine(["g"], keep="last")
        eng.add_batch(schema, [(math.nan, 1), (math.nan, 2)])
        self.assertEqual(len(eng.result()), 1)
        self.assertEqual(eng.result()[0][1], 2)

    def test_negative_zero_equals_zero(self):
        schema = [("g", "float"), ("v", "int")]
        eng = DedupEngine(["g"], keep="first")
        eng.add_batch(schema, [(-0.0, 1), (0.0, 2)])
        self.assertEqual(eng.result(), [(-0.0, 1)])

    def test_null_equal_under_forced_collision(self):
        schema = [("g", "str"), ("v", "int")]
        eng = DedupEngine(["g"], keep="first", hash_fn=force_collision)
        eng.add_batch(schema, [(None, 1), (None, 2)])
        self.assertEqual(eng.result(), [(None, 1)])


class HashCollisionTest(unittest.TestCase):
    def test_forced_collision_still_distinguishes_values(self):
        eng = DedupEngine(["id"], keep="first", hash_fn=force_collision)
        eng.add_batch(SCHEMA, [
            (1, "a", 1.0),
            (2, "b", 2.0),
            (1, "a2", 3.0),
            (3, "c", 3.0),
        ])
        self.assertEqual(eng.result(),
                         [(1, "a", 1.0), (2, "b", 2.0), (3, "c", 3.0)])

    def test_forced_collision_keep_last(self):
        eng = DedupEngine(["id"], keep="last", hash_fn=force_collision)
        eng.add_batch(SCHEMA, [(1, "a", 1.0), (2, "b", 2.0), (1, "a2", 3.0)])
        self.assertEqual(eng.result(), [(2, "b", 2.0), (1, "a2", 3.0)])

    def test_hash_table_unit(self):
        table = HashTable(hash_fn=force_collision)
        table.set(("int", 1), "one")
        table.set(("int", 2), "two")
        table.set(("int", 1), "uno")  # 同键覆盖
        self.assertEqual(len(table), 2)
        self.assertEqual(table.get(("int", 1)), "uno")
        self.assertEqual(table.get(("int", 2)), "two")
        self.assertIsNone(table.get(("int", 3)))
        self.assertIn(("int", 2), table)
        self.assertNotIn(("int", 9), table)


class ExactIntTest(unittest.TestCase):
    def test_big_ints_exact(self):
        # 超过 float 精确范围的整数必须精确区分
        schema = [("g", "int"), ("v", "int")]
        eng = DedupEngine(["g"], keep="first")
        big = 2 ** 63
        eng.add_batch(schema, [(big, 1), (big + 1, 2), (big, 3)])
        self.assertEqual(eng.result(), [(big, 1), (big + 1, 2)])

    def test_huge_int_no_float_rounding(self):
        schema = [("g", "int"), ("v", "int")]
        eng = DedupEngine(["g"], keep="first")
        a = 10 ** 30
        eng.add_batch(schema, [(a, 1), (a + 1, 2)])
        self.assertEqual(len(eng.result()), 2)


class EmptyKeyTest(unittest.TestCase):
    def test_empty_key_columns_single_group_first(self):
        eng = DedupEngine([], keep="first")
        eng.add_batch(SCHEMA, [(1, "a", 1.0), (2, "b", 2.0)])
        eng.add_batch(SCHEMA, [(3, "c", 3.0)])
        self.assertEqual(eng.result(), [(1, "a", 1.0)])

    def test_empty_key_columns_single_group_last(self):
        eng = DedupEngine([], keep="last")
        eng.add_batch(SCHEMA, [(1, "a", 1.0), (2, "b", 2.0)])
        eng.add_batch(SCHEMA, [(3, "c", 3.0)])
        self.assertEqual(eng.result(), [(3, "c", 3.0)])


class MultiColumnKeyTest(unittest.TestCase):
    def test_composite_key(self):
        eng = DedupEngine(["id", "name"], keep="first")
        eng.add_batch(SCHEMA, [
            (1, "a", 1.0),
            (1, "b", 2.0),   # name 不同 -> 不同键
            (1, "a", 3.0),   # 完全同键 -> 去重
        ])
        self.assertEqual(eng.result(), [(1, "a", 1.0), (1, "b", 2.0)])


class ValidationTest(unittest.TestCase):
    def test_bad_keep_value(self):
        with self.assertRaises(ValueError):
            DedupEngine(["id"], keep="middle")

    def test_unknown_key_column(self):
        eng = DedupEngine(["nope"])
        with self.assertRaises(SchemaError):
            eng.add_batch(SCHEMA, [(1, "a", 1.0)])

    def test_duplicate_column_names(self):
        eng = DedupEngine(["id"])
        with self.assertRaises(SchemaError):
            eng.add_batch([("id", "int"), ("id", "int")], [(1, 2)])

    def test_unsupported_column_type(self):
        eng = DedupEngine(["id"])
        with self.assertRaises(SchemaError):
            eng.add_batch([("id", "decimal")], [(1,)])

    def test_schema_mismatch_between_batches(self):
        eng = DedupEngine(["id"])
        eng.add_batch(SCHEMA, [(1, "a", 1.0)])
        with self.assertRaises(SchemaError):
            eng.add_batch([("id", "int"), ("name", "str")], [(1, "a")])

    def test_row_width_mismatch(self):
        eng = DedupEngine(["id"])
        with self.assertRaises(RowTypeError):
            eng.add_batch(SCHEMA, [(1, "a")])

    def test_type_mismatch(self):
        eng = DedupEngine(["id"])
        with self.assertRaises(RowTypeError):
            eng.add_batch(SCHEMA, [("not-an-int", "a", 1.0)])

    def test_bool_not_accepted_as_int(self):
        eng = DedupEngine(["id"])
        with self.assertRaises(RowTypeError):
            eng.add_batch(SCHEMA, [(True, "a", 1.0)])

    def test_int_not_accepted_as_float(self):
        eng = DedupEngine(["score"])
        with self.assertRaises(RowTypeError):
            eng.add_batch(SCHEMA, [(1, "a", 1)])

    def test_none_allowed_in_any_column(self):
        eng = DedupEngine(["id"], keep="first")
        eng.add_batch(SCHEMA, [(None, None, None), (None, "x", 2.0)])
        self.assertEqual(eng.result(), [(None, None, None)])


if __name__ == "__main__":
    unittest.main(verbosity=2)
