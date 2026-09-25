import math
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from eqjoin import NULL, ColType, RecordBatch, join

INT = ColType.INT
FLOAT = ColType.FLOAT
TEXT = ColType.TEXT

LSCHEMA = (("k", INT), ("lv", TEXT))
RSCHEMA = (("k", INT), ("rv", TEXT))


def batches_of(schema, rows, size=3):
    return [RecordBatch(schema, rows[i:i + size])
            for i in range(0, max(len(rows), 1), size)] or \
           [RecordBatch(schema, [])]


def matchable(key):
    return all(v is not NULL and not (isinstance(v, float) and math.isnan(v))
               for v in key)


def reference(left, right, how):
    """Naive nested-loop join defining the expected 'left' order."""
    out = []
    matched_r = set()
    for li, lrow in enumerate(left):
        hit = False
        for ri, rrow in enumerate(right):
            lk, rk = lrow[:1], rrow[:1]
            if matchable(lk) and matchable(rk) and lk == rk:
                hit = True
                matched_r.add(ri)
                out.append(lrow + rrow)
        if not hit and how in ("left", "full"):
            out.append(lrow + (NULL,) * len(RSCHEMA))
    if how == "full":
        for ri, rrow in enumerate(right):
            if ri not in matched_r:
                out.append((NULL,) * len(LSCHEMA) + rrow)
    return out


def run(left, right, how, **kw):
    kw.setdefault("left_on", "k")
    kw.setdefault("right_on", "k")
    return join(batches_of(LSCHEMA, left), batches_of(RSCHEMA, right),
                how=how, **kw).rows()


class TestSemantics(unittest.TestCase):
    def test_cartesian_multiplicity_2x3(self):
        left = [(7, "l1"), (7, "l2")]
        right = [(7, "r1"), (7, "r2"), (7, "r3")]
        out = run(left, right, "inner")
        self.assertEqual(len(out), 6)
        self.assertEqual(out, [(7, "l1", 7, "r1"), (7, "l1", 7, "r2"),
                               (7, "l1", 7, "r3"), (7, "l2", 7, "r1"),
                               (7, "l2", 7, "r2"), (7, "l2", 7, "r3")])

    def test_null_never_matches_inner(self):
        out = run([(NULL, "a"), (1, "b")], [(NULL, "x"), (2, "y")], "inner")
        self.assertEqual(out, [])

    def test_left_join_keeps_null_left(self):
        out = run([(NULL, "a"), (1, "b")], [(1, "x")], "left")
        self.assertEqual(out, [(NULL, "a", NULL, NULL), (1, "b", 1, "x")])

    def test_full_join_keeps_nulls_both_sides(self):
        out = run([(NULL, "a"), (1, "b")], [(NULL, "x"), (1, "y")], "full")
        self.assertEqual(out, [
            (NULL, "a", NULL, NULL),   # unmatched NULL-keyed left row
            (1, "b", 1, "y"),          # match
            (NULL, NULL, NULL, "x"),   # unmatched NULL-keyed right row
        ])

    def test_nan_is_not_null_and_nan_never_matches(self):
        nan = float("nan")
        ls = (("k", FLOAT), ("lv", TEXT))
        rs = (("k", FLOAT), ("rv", TEXT))
        left = [RecordBatch(ls, [(nan, "a"), (1.5, "b")])]
        right = [RecordBatch(rs, [(nan, "x"), (1.5, "y")])]
        inner = join(left, right, left_on="k", right_on="k", how="inner")
        self.assertEqual(inner.rows(), [(1.5, "b", 1.5, "y")])
        full = join([RecordBatch(ls, [(nan, "a")])],
                    [RecordBatch(rs, [(nan, "x")])],
                    left_on="k", right_on="k", how="full").rows()
        self.assertEqual(len(full), 2)
        self.assertTrue(math.isnan(full[0][0]))        # NaN preserved as value
        self.assertIs(full[0][2], NULL)                # right side NULL-filled
        self.assertIs(full[1][0], NULL)
        self.assertTrue(math.isnan(full[1][2]))
        self.assertNotEqual(str(full[0][0]), "NULL")   # NaN != NULL

    def test_int_exact_arbitrary_precision(self):
        big = 2 ** 70 + 1
        out = run([(big, "a")], [(big, "x"), (2 ** 70, "y")], "inner")
        self.assertEqual(out, [(big, "a", big, "x")])

    def test_unmatched_right_appended_in_right_order(self):
        left = [(1, "a")]
        right = [(5, "r5"), (1, "r1"), (3, "r3")]
        out = run(left, right, "full")
        self.assertEqual(out, [(1, "a", 1, "r1"),
                               (NULL, NULL, 5, "r5"),
                               (NULL, NULL, 3, "r3")])

    def test_empty_inputs(self):
        for how in ("inner", "left", "full"):
            self.assertEqual(run([], [], how), [])
        self.assertEqual(run([(1, "a")], [], "left"),
                         [(1, "a", NULL, NULL)])
        self.assertEqual(run([], [(2, "x")], "full"),
                         [(NULL, NULL, 2, "x")])


class TestScaling(unittest.TestCase):
    def test_all_keys_identical_no_infinite_recursion(self):
        left = [(1, f"l{i}") for i in range(40)]
        right = [(1, f"r{i}") for i in range(30)]
        res = join(batches_of(LSCHEMA, left, 7), batches_of(RSCHEMA, right, 5),
                   left_on="k", right_on="k", how="inner",
                   num_partitions=2, max_rows_in_memory=4)
        self.assertEqual(len(res), 40 * 30)
        self.assertGreaterEqual(res.stats["block_nested_loop_partitions"], 1)
        self.assertEqual(res.rows(), reference(left, right, "inner"))

    def test_spill_matches_in_memory_all_join_types(self):
        rng = random.Random(42)
        domain = [1, 2, 3, 4, NULL]
        left = [(rng.choice(domain), f"l{i}") for i in range(60)]
        right = [(rng.choice(domain), f"r{i}") for i in range(50)]
        for how in ("inner", "left", "full"):
            big = run(left, right, how, max_rows_in_memory=10**6)
            small = run(left, right, how, num_partitions=3,
                        max_rows_in_memory=4)
            self.assertEqual(small, big, how)
            self.assertEqual(small, reference(left, right, how), how)

    def test_spill_actually_happened(self):
        left = [(i % 5, f"l{i}") for i in range(50)]
        right = [(i % 5, f"r{i}") for i in range(50)]
        res = join(batches_of(LSCHEMA, left), batches_of(RSCHEMA, right),
                   left_on="k", right_on="k", how="inner",
                   num_partitions=2, max_rows_in_memory=4)
        self.assertGreater(res.stats["spilled_partitions"], 0)

    def test_partition_order_is_deterministic_and_same_rows(self):
        rng = random.Random(7)
        left = [(rng.randrange(6), f"l{i}") for i in range(40)]
        right = [(rng.randrange(6), f"r{i}") for i in range(40)]
        kw = dict(left_on="k", right_on="k", how="full",
                  num_partitions=4, max_rows_in_memory=8)
        a = join(batches_of(LSCHEMA, left), batches_of(RSCHEMA, right),
                 order="partition", **kw).rows()
        b = join(batches_of(LSCHEMA, left), batches_of(RSCHEMA, right),
                 order="partition", **kw).rows()
        c = join(batches_of(LSCHEMA, left), batches_of(RSCHEMA, right),
                 order="left", **kw).rows()
        self.assertEqual(a, b)                       # deterministic
        self.assertEqual(sorted(map(str, a)), sorted(map(str, c)))  # same set

    def test_multi_batch_equals_single_batch(self):
        rows_l = [(i % 3, f"l{i}") for i in range(20)]
        rows_r = [(i % 3, f"r{i}") for i in range(20)]
        one = run(rows_l, rows_r, "full")
        many = join(batches_of(LSCHEMA, rows_l, 1),
                    batches_of(RSCHEMA, rows_r, 2),
                    left_on="k", right_on="k", how="full").rows()
        self.assertEqual(one, many)


class TestValidation(unittest.TestCase):
    def test_type_checking(self):
        with self.assertRaises(TypeError):
            RecordBatch((("k", INT),), [(1.5,)])
        with self.assertRaises(TypeError):
            RecordBatch((("k", INT),), [(True,)])
        with self.assertRaises(TypeError):
            RecordBatch((("k", FLOAT),), [(1,)])
        with self.assertRaises(TypeError):
            RecordBatch((("k", TEXT),), [(1,)])

    def test_key_type_mismatch_rejected(self):
        left = [RecordBatch((("k", INT),), [(1,)])]
        right = [RecordBatch((("k", FLOAT),), [(1.0,)])]
        with self.assertRaises(TypeError):
            join(left, right, left_on="k", right_on="k")

    def test_unknown_key_column(self):
        with self.assertRaises(KeyError):
            join([RecordBatch(LSCHEMA, [])], [RecordBatch(RSCHEMA, [])],
                 left_on="nope", right_on="k")

    def test_bad_how_and_order(self):
        args = ([RecordBatch(LSCHEMA, [])], [RecordBatch(RSCHEMA, [])])
        with self.assertRaises(ValueError):
            join(*args, left_on="k", right_on="k", how="cross")
        with self.assertRaises(ValueError):
            join(*args, left_on="k", right_on="k", order="random")


if __name__ == "__main__":
    unittest.main()
