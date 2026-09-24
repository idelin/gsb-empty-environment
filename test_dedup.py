"""dedup 引擎的 unittest 测试套件（Python 3.11，仅标准库）。"""

import math
import unittest

from dedup import Column, DedupEngine, HashIndex


def schema3():
    return [
        Column("id", "int"),
        Column("name", "str"),
        Column("score", "float"),
    ]


class KeepFirstTest(unittest.TestCase):
    def test_keep_first_basic(self):
        engine = DedupEngine(schema3(), key_columns=["id"], keep="first")
        engine.add_batch([
            (1, "alice", 90.0),
            (2, "bob", 80.0),
            (1, "alice-v2", 99.0),   # 重复 id=1，丢弃
            (3, "carol", 70.0),
        ])
        self.assertEqual(
            engine.result(),
            [(1, "alice", 90.0), (2, "bob", 80.0), (3, "carol", 70.0)],
        )

    def test_keep_first_across_batches(self):
        engine = DedupEngine(schema3(), key_columns=["id"], keep="first")
        engine.add_batch([(1, "a", 1.0)])
        engine.add_batch([(1, "b", 2.0), (2, "c", 3.0)])
        self.assertEqual(engine.result(), [(1, "a", 1.0), (2, "c", 3.0)])


class KeepLastTest(unittest.TestCase):
    def test_keep_last_updates_in_declared_event_order(self):
        """保留末条：对旧 ID 的更新严格按 add_batch 声明的事件序生效。"""
        engine = DedupEngine(schema3(), key_columns=["id"], keep="last")
        engine.add_batch([(1, "v1", 1.0), (2, "x", 0.0)])
        engine.add_batch([(1, "v2", 2.0)])
        engine.add_batch([(1, "v3", 3.0)])
        rows = engine.result()
        self.assertEqual(rows, [(2, "x", 0.0), (1, "v3", 3.0)])

    def test_result_order_by_kept_row_original_seq(self):
        """结果顺序由被保留行的原始序号决定，而非键顺序。"""
        engine = DedupEngine(schema3(), key_columns=["id"], keep="last")
        engine.add_batch([(1, "a", 1.0), (2, "b", 2.0)])   # seq 0, 1
        engine.add_batch([(1, "a2", 9.0)])                  # seq 2，替换 id=1
        # id=2 保留行 seq=1，id=1 保留行 seq=2 -> 先 id=2 后 id=1
        self.assertEqual(engine.result(), [(2, "b", 2.0), (1, "a2", 9.0)])

    def test_keep_last_within_single_batch(self):
        engine = DedupEngine(schema3(), key_columns=["id"], keep="last")
        engine.add_batch([(1, "a", 1.0), (1, "b", 2.0), (1, "c", 3.0)])
        self.assertEqual(engine.result(), [(1, "c", 3.0)])


class NullAndNanTest(unittest.TestCase):
    def test_nulls_are_equal_for_dedup(self):
        engine = DedupEngine(
            [Column("k", "str"), Column("v", "int")],
            key_columns=["k"], keep="first",
        )
        engine.add_batch([(None, 1), (None, 2), ("x", 3)])
        self.assertEqual(engine.result(), [(None, 1), ("x", 3)])

    def test_null_and_nan_are_different(self):
        engine = DedupEngine(
            [Column("k", "float"), Column("v", "str")],
            key_columns=["k"], keep="first",
        )
        engine.add_batch([(None, "null"), (float("nan"), "nan")])
        self.assertEqual(len(engine.result()), 2)
        self.assertEqual(engine.result()[0], (None, "null"))
        self.assertEqual(engine.result()[1][1], "nan")
        self.assertTrue(math.isnan(engine.result()[1][0]))

    def test_nan_equals_nan_for_dedup(self):
        engine = DedupEngine(
            [Column("k", "float"), Column("v", "str")],
            key_columns=["k"], keep="first",
        )
        engine.add_batch([
            (float("nan"), "nan-1"),
            (float("nan"), "nan-2"),   # 同组，丢弃
            (1.5, "x"),
        ])
        self.assertEqual(len(engine.result()), 2)

    def test_null_in_int_column(self):
        engine = DedupEngine(
            [Column("k", "int"), Column("v", "str")],
            key_columns=["k"], keep="first",
        )
        engine.add_batch([(None, "a"), (None, "b"), (7, "c")])
        self.assertEqual(engine.result(), [(None, "a"), (7, "c")])


class IntegerExactnessTest(unittest.TestCase):
    def test_big_integers_exact(self):
        """整数精确计算：超出 float53 的整数不得被混为一谈。"""
        engine = DedupEngine(
            [Column("k", "int"), Column("v", "str")],
            key_columns=["k"], keep="first",
        )
        engine.add_batch([
            (2**63, "a"),
            (2**63 + 1, "b"),       # float 无法区分，必须保留
            (2**63, "dup"),          # 真重复，丢弃
            (10**30, "c"),
            (10**30 + 1, "d"),
        ])
        values = [v for _, v in engine.result()]
        self.assertEqual(values, ["a", "b", "c", "d"])

    def test_negative_and_zero(self):
        engine = DedupEngine(
            [Column("k", "int"), Column("v", "str")],
            key_columns=["k"], keep="first",
        )
        engine.add_batch([(0, "zero"), (-1, "neg"), (0, "dup")])
        self.assertEqual(engine.result(), [(0, "zero"), (-1, "neg")])


class HashCollisionTest(unittest.TestCase):
    def test_forced_constant_hash_still_distinguishes_values(self):
        """强制所有键哈希相同，值仍须逐一区分。"""
        engine = DedupEngine(
            [Column("k", "int"), Column("v", "str")],
            key_columns=["k"], keep="first",
            hash_fn=lambda key: 0,   # 全部碰撞到同一个桶
        )
        engine.add_batch([(i, f"v{i}") for i in range(200)])
        engine.add_batch([(i, f"dup{i}") for i in range(0, 200, 2)])
        result = engine.result()
        self.assertEqual(len(result), 200)
        self.assertEqual(result[37], (37, "v37"))

    def test_hash_index_bucket_correctness(self):
        index = HashIndex(hash_fn=lambda key: 42)
        for i in range(100):
            index.put((i, "k"), i)
        for i in range(100):
            self.assertEqual(index.get((i, "k")), i)
        self.assertIsNone(index.get((1000, "k")))
        index.put((5, "k"), 555)     # 覆盖同键
        self.assertEqual(index.get((5, "k")), 555)
        self.assertEqual(len(index), 100)

    def test_collision_with_keep_last(self):
        engine = DedupEngine(
            [Column("k", "str"), Column("v", "int")],
            key_columns=["k"], keep="last",
            hash_fn=lambda key: 7,
        )
        engine.add_batch([("a", 1), ("b", 2), ("a", 3)])
        self.assertEqual(engine.result(), [("b", 2), ("a", 3)])


class EmptyKeyColumnsTest(unittest.TestCase):
    def test_empty_key_columns_keep_first(self):
        """空键列：全表同一组，保留首条。"""
        engine = DedupEngine(schema3(), key_columns=[], keep="first")
        engine.add_batch([(1, "a", 1.0), (2, "b", 2.0)])
        engine.add_batch([(3, "c", 3.0)])
        self.assertEqual(engine.result(), [(1, "a", 1.0)])

    def test_empty_key_columns_keep_last(self):
        """空键列：全表同一组，保留末条（跨批次）。"""
        engine = DedupEngine(schema3(), key_columns=[], keep="last")
        engine.add_batch([(1, "a", 1.0), (2, "b", 2.0)])
        engine.add_batch([(3, "c", 3.0)])
        self.assertEqual(engine.result(), [(3, "c", 3.0)])


class MultiColumnKeyTest(unittest.TestCase):
    def test_composite_key(self):
        engine = DedupEngine(schema3(), key_columns=["id", "name"], keep="first")
        engine.add_batch([
            (1, "a", 1.0),
            (1, "b", 2.0),   # 同 id 不同 name：不同组
            (1, "a", 9.0),   # 完全同键：丢弃
        ])
        self.assertEqual(engine.result(), [(1, "a", 1.0), (1, "b", 2.0)])

    def test_bool_and_str_keys(self):
        schema = [Column("flag", "bool"), Column("tag", "str"), Column("v", "int")]
        engine = DedupEngine(schema, key_columns=["flag", "tag"], keep="first")
        engine.add_batch([
            (True, "x", 1),
            (False, "x", 2),
            (True, "x", 3),   # 重复，丢弃
            (True, "y", 4),
        ])
        self.assertEqual(
            engine.result(),
            [(True, "x", 1), (False, "x", 2), (True, "y", 4)],
        )


class ValidationTest(unittest.TestCase):
    def test_unknown_key_column(self):
        with self.assertRaises(ValueError):
            DedupEngine(schema3(), key_columns=["nope"])

    def test_invalid_keep_mode(self):
        with self.assertRaises(ValueError):
            DedupEngine(schema3(), key_columns=["id"], keep="middle")

    def test_row_width_mismatch(self):
        engine = DedupEngine(schema3(), key_columns=["id"])
        with self.assertRaises(ValueError):
            engine.add_batch([(1, "a")])

    def test_batch_schema_mismatch(self):
        engine = DedupEngine(schema3(), key_columns=["id"])
        with self.assertRaises(ValueError):
            engine.add_batch([(1, "a", 1.0)],
                             schema=[Column("id", "int"),
                                     Column("name", "str"),
                                     Column("score", "str")])

    def test_nan_rejected_in_int_column(self):
        engine = DedupEngine(
            [Column("k", "int"), Column("v", "str")], key_columns=["k"])
        with self.assertRaises(TypeError):
            engine.add_batch([(float("nan"), "x")])

    def test_unknown_column_type(self):
        with self.assertRaises(ValueError):
            Column("c", "blob")


class FloatColumnTest(unittest.TestCase):
    def test_float_key_grouping(self):
        engine = DedupEngine(
            [Column("k", "float"), Column("v", "str")],
            key_columns=["k"], keep="first",
        )
        engine.add_batch([(1.0, "a"), (1, "b"), (2.5, "c")])
        # float 列中 1 与 1.0 规范化相同 -> 同组
        self.assertEqual(engine.result(), [(1.0, "a"), (2.5, "c")])


if __name__ == "__main__":
    unittest.main(verbosity=2)
