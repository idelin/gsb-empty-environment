import math
import os
import shutil
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from minijoin import (
    NULL,
    Batch,
    ColType,
    Joiner,
    JoinType,
    OutputOrder,
    Schema,
)


def emp_schema():
    return Schema([("id", ColType.INT), ("dept", ColType.INT), ("name", ColType.STR)])


def dept_schema():
    return Schema([("dept", ColType.INT), ("building", ColType.STR)])


def batches_of(schema, rows, size):
    return [Batch(schema, rows[i : i + size]) for i in range(0, len(rows), size)] or [
        Batch(schema, [])
    ]


def run(join_type, left_rows, right_rows, left_schema=None, right_schema=None,
        left_keys=("dept",), right_keys=("dept",), batch_size=3, **kw):
    ls = left_schema or emp_schema()
    rs = right_schema or dept_schema()
    joiner = Joiner(ls, rs, left_keys, right_keys, join_type=join_type, **kw)
    return joiner.collect(
        batches_of(ls, left_rows, batch_size),
        batches_of(rs, right_rows, batch_size),
    )


class TestInnerJoin(unittest.TestCase):
    def test_cartesian_multiplicity_2x3_gives_6(self):
        left = [(1, 10, "a"), (2, 10, "b"), (3, 20, "c")]
        right = [(10, "x"), (10, "y"), (10, "z"), (30, "w")]
        rows = run(JoinType.INNER, left, right)
        self.assertEqual(len(rows), 6)
        self.assertEqual(
            rows,
            [
                (1, 10, "a", 10, "x"),
                (1, 10, "a", 10, "y"),
                (1, 10, "a", 10, "z"),
                (2, 10, "b", 10, "x"),
                (2, 10, "b", 10, "y"),
                (2, 10, "b", 10, "z"),
            ],
        )

    def test_inner_drops_null_keys_and_unmatched(self):
        left = [(1, NULL, "a"), (2, 10, "b")]
        right = [(NULL, "x"), (10, "y")]
        rows = run(JoinType.INNER, left, right)
        self.assertEqual(rows, [(2, 10, "b", 10, "y")])

    def test_empty_inputs(self):
        self.assertEqual(run(JoinType.INNER, [], []), [])
        self.assertEqual(run(JoinType.INNER, [(1, 1, "a")], []), [])
        self.assertEqual(run(JoinType.LEFT, [], [(1, "x")]), [])
        full = run(JoinType.FULL, [], [(5, "x")])
        self.assertEqual(full, [(NULL, NULL, NULL, 5, "x")])


class TestOuterJoins(unittest.TestCase):
    def test_left_join_keeps_unmatched_and_null_left(self):
        left = [(1, 10, "a"), (2, 20, "b"), (3, NULL, "c")]
        right = [(10, "x"), (NULL, "y")]
        rows = run(JoinType.LEFT, left, right)
        self.assertEqual(
            rows,
            [
                (1, 10, "a", 10, "x"),
                (2, 20, "b", NULL, NULL),
                (3, NULL, "c", NULL, NULL),
            ],
        )

    def test_full_join_keeps_both_sides_nulls_included(self):
        left = [(1, 10, "a"), (2, NULL, "b")]
        right = [(10, "x"), (NULL, "y"), (30, "z")]
        rows = run(JoinType.FULL, left, right)
        self.assertEqual(
            rows,
            [
                (1, 10, "a", 10, "x"),          # matched
                (2, NULL, "b", NULL, NULL),     # left NULL key kept
                (NULL, NULL, NULL, NULL, "y"),  # right NULL key kept
                (NULL, NULL, NULL, 30, "z"),    # right unmatched kept
            ],
        )

    def test_null_never_matches_null(self):
        left = [(i, NULL, f"l{i}") for i in range(4)]
        right = [(NULL, f"r{i}") for i in range(3)]
        self.assertEqual(run(JoinType.INNER, left, right), [])
        rows = run(JoinType.FULL, left, right)
        self.assertEqual(len(rows), 7)  # 4 + 3, no pairing


class TestNullVsNaN(unittest.TestCase):
    def setUp(self):
        self.ls = Schema([("k", ColType.FLOAT), ("v", ColType.STR)])
        self.rs = Schema([("k", ColType.FLOAT), ("w", ColType.STR)])

    def run_float(self, left_rows, right_rows, join_type=JoinType.INNER):
        joiner = Joiner(self.ls, self.rs, ["k"], ["k"], join_type=join_type)
        return joiner.collect(
            [Batch(self.ls, left_rows)], [Batch(self.rs, right_rows)]
        )

    def test_nan_is_not_null_and_matches_nan(self):
        left = [(float("nan"), "a"), (NULL, "b"), (1.5, "c")]
        right = [(float("nan"), "x"), (float("nan"), "y"), (NULL, "z")]
        rows = self.run_float(left, right)
        self.assertEqual(len(rows), 2)  # 1 NaN x 2 NaN
        for row in rows:
            self.assertTrue(math.isnan(row[0]))
            self.assertTrue(math.isnan(row[2]))
        self.assertEqual({row[1] for row in rows}, {"a"})
        self.assertEqual({row[3] for row in rows}, {"x", "y"})

    def test_null_does_not_match_nan(self):
        rows = self.run_float([(NULL, "a")], [(float("nan"), "x")])
        self.assertEqual(rows, [])


class TestExactIntegers(unittest.TestCase):
    def test_big_ints_match_exactly(self):
        big = 2**70 + 12345
        ls = Schema([("k", ColType.INT)])
        rs = Schema([("k", ColType.INT)])
        joiner = Joiner(ls, rs, ["k"], ["k"])
        rows = joiner.collect(
            [Batch(ls, [(big,), (big + 1,), (2**53 + 1,)])],
            [Batch(rs, [(big,), (big + 2,), (2**53,)])],
        )
        self.assertEqual(rows, [(big, big)])

    def test_bool_is_not_int_key(self):
        with self.assertRaises(TypeError):
            Batch(Schema([("k", ColType.INT)]), [(True,)])


class TestSpillAndOrdering(unittest.TestCase):
    def setUp(self):
        # Deterministic pseudo-random data with heavy key repetition.
        self.left = [(i, (i * 37) % 11, f"l{i}") for i in range(120)]
        self.right = [((i * 13) % 11, f"r{i}") for i in range(80)]

    def test_spill_results_identical_to_in_memory(self):
        big = run(JoinType.FULL, self.left, self.right, memory_limit_rows=10**9)
        small = run(
            JoinType.FULL,
            self.left,
            self.right,
            memory_limit_rows=7,
            num_partitions=4,
        )
        self.assertEqual(big, small)  # same rows, same defined order

    def test_left_order_preserved_under_spill(self):
        rows = run(
            JoinType.LEFT,
            self.left,
            self.right,
            memory_limit_rows=5,
            num_partitions=3,
            output_order=OutputOrder.LEFT,
        )
        left_ids = [row[0] for row in rows]
        self.assertEqual(left_ids, sorted(left_ids))

    def test_spill_files_are_actually_written(self):
        tmp = tempfile.mkdtemp(prefix="minijoin-spill-")
        self.addCleanup(shutil.rmtree, tmp, True)
        seen = []
        flushes = []

        from minijoin import joiner as joiner_mod

        orig_flush = joiner_mod._SpillPartition.flush

        def counting_flush(self_part):
            if self_part._buffer:
                flushes.append(self_part._name)
            orig_flush(self_part)

        self.addCleanup(setattr, joiner_mod._SpillPartition, "flush", orig_flush)
        joiner_mod._SpillPartition.flush = counting_flush

        class CountingJoiner(Joiner):
            def _in_memory_join(self, l, r, out):
                seen.append((len(l), len(r)))
                super()._in_memory_join(l, r, out)

        joiner = CountingJoiner(
            emp_schema(), dept_schema(), ["dept"], ["dept"],
            join_type=JoinType.INNER, memory_limit_rows=6, num_partitions=4,
            tmp_dir=tmp,
        )
        joiner.collect(batches_of(emp_schema(), self.left, 10),
                       batches_of(dept_schema(), self.right, 10))
        # Buffers really spilled to disk under the 6-row budget.
        self.assertTrue(flushes)
        # With a 6-row budget and 200 rows, joins must have happened in
        # small partition chunks rather than one big in-memory pass.
        self.assertTrue(seen)
        self.assertTrue(all(l + r <= 200 for l, r in seen))
        self.assertGreater(len(seen), 1)

    def test_all_keys_identical_no_infinite_recursion(self):
        left = [(i, 1, f"l{i}") for i in range(60)]
        right = [(1, f"r{i}") for i in range(40)]
        rows = run(
            JoinType.INNER,
            left,
            right,
            memory_limit_rows=4,
            num_partitions=2,
            max_depth=4,
        )
        self.assertEqual(len(rows), 60 * 40)

    def test_all_keys_identical_full_outer(self):
        left = [(i, 7, f"l{i}") for i in range(5)]
        right = [(7, f"r{i}") for i in range(3)]
        rows = run(
            JoinType.FULL, left, right, memory_limit_rows=2, num_partitions=2
        )
        self.assertEqual(len(rows), 15)  # all match; nothing dangling


class TestBatchesAndTypes(unittest.TestCase):
    def test_many_small_batches_equal_one_big_batch(self):
        left = [(i, i % 5, f"l{i}") for i in range(50)]
        right = [(i % 5, f"r{i}") for i in range(30)]
        one = run(JoinType.LEFT, left, right, batch_size=10**6)
        many = run(JoinType.LEFT, left, right, batch_size=1)
        self.assertEqual(one, many)

    def test_multi_column_keys(self):
        ls = Schema([("a", ColType.INT), ("b", ColType.STR), ("v", ColType.INT)])
        rs = Schema([("x", ColType.INT), ("y", ColType.STR)])
        joiner = Joiner(ls, rs, ["a", "b"], ["x", "y"])
        rows = joiner.collect(
            [Batch(ls, [(1, "k", 100), (1, "j", 200), (1, "k", 300)])],
            [Batch(rs, [(1, "k"), (1, "k")])],
        )
        self.assertEqual(
            rows,
            [(1, "k", 100, 1, "k"), (1, "k", 100, 1, "k"),
             (1, "k", 300, 1, "k"), (1, "k", 300, 1, "k")],
        )

    def test_type_validation(self):
        schema = Schema([("k", ColType.INT)])
        with self.assertRaises(TypeError):
            Batch(schema, [("not-an-int",)])
        with self.assertRaises(ValueError):
            Batch(schema, [(1, 2)])
        with self.assertRaises(TypeError):
            Joiner(schema, Schema([("k", ColType.STR)]), ["k"], ["k"])
        with self.assertRaises(ValueError):
            Joiner(schema, schema, ["k"], ["k"], num_partitions=1)

    def test_output_schema_renames_collisions(self):
        joiner = Joiner(emp_schema(), dept_schema(), ["dept"], ["dept"])
        names = [c.name for c in joiner.output_schema.columns]
        self.assertEqual(names, ["id", "dept", "name", "right.dept", "building"])


if __name__ == "__main__":
    unittest.main()
