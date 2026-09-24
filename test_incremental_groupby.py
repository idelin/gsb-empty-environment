import math
import unittest

from incremental_groupby import (
    FLOAT,
    INT,
    NAN_KEY,
    STR,
    Batch,
    GroupedAggregate,
)


def make_table(key_type=STR, value_type=FLOAT, **kw):
    return GroupedAggregate(key_type=key_type, value_type=value_type, **kw)


def row_batch(rows, key_type=STR, value_type=FLOAT):
    return Batch(
        [("id", INT), ("key", key_type), ("value", value_type)], rows
    )


class TestBasicAggregation(unittest.TestCase):
    def test_insert_groups(self):
        t = make_table()
        t.insert_batch(row_batch([
            (1, "a", 10.0),
            (2, "a", 4.0),
            (3, "b", 7.5),
            (4, "a", 6.0),
        ]))
        a = t.get("a")
        self.assertEqual((a.count, a.sum, a.min), (3, 20.0, 4.0))
        b = t.get("b")
        self.assertEqual((b.count, b.sum, b.min), (1, 7.5, 7.5))
        self.assertEqual(len(t), 4)

    def test_count_is_count_star(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", None), (2, "a", 5.0)]))
        a = t.get("a")
        self.assertEqual(a.count, 2)          # null row still counted
        self.assertEqual(a.non_null_count, 1)
        self.assertEqual((a.sum, a.min), (5.0, 5.0))


class TestNullAndNaN(unittest.TestCase):
    def test_all_null_group_sum_is_null_but_count_counts_rows(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", None), (2, "a", None), (3, "a", None)]))
        a = t.get("a")
        self.assertEqual(a.count, 3)
        self.assertIsNone(a.sum)
        self.assertIsNone(a.min)

    def test_null_key_group(self):
        t = make_table()
        t.insert_batch(row_batch([(1, None, 1.0), (2, None, 2.0), (3, "a", 9.0)]))
        null_group = t.get(None)
        self.assertEqual((null_group.count, null_group.sum, null_group.min), (2, 3.0, 1.0))
        keys = [r.key for r in t.result()]
        self.assertIn(None, keys)
        self.assertIn("a", keys)

    def test_nan_differs_from_null(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", None), (2, "a", float("nan"))]))
        a = t.get("a")
        self.assertEqual(a.count, 2)
        self.assertEqual(a.non_null_count, 1)  # NaN is a value, null is not
        self.assertTrue(math.isnan(a.sum))     # NaN propagates into sum
        self.assertIsNone(a.min)               # NaN ignored by min

    def test_nan_removed_restores_sum(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", float("nan")), (2, "a", 3.0)]))
        self.assertTrue(math.isnan(t.get("a").sum))
        t.delete_batch(Batch([("id", INT)], [(1,)]))
        a = t.get("a")
        self.assertEqual((a.count, a.sum, a.min), (1, 3.0, 3.0))

    def test_nan_ignored_by_min(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", float("nan")), (2, "a", 5.0), (3, "a", 2.0)]))
        self.assertEqual(t.get("a").min, 2.0)

    def test_nan_keys_form_single_group_distinct_from_null(self):
        t = make_table(key_type=FLOAT, value_type=INT)
        t.insert_batch(Batch(
            [("id", INT), ("key", FLOAT), ("value", INT)],
            [(1, float("nan"), 1), (2, float("nan"), 2), (3, None, 10), (4, 1.5, 100)],
        ))
        nan_group = t.get(float("nan"))
        self.assertEqual((nan_group.count, nan_group.sum), (2, 3))
        self.assertIs(nan_group.key, NAN_KEY)
        null_group = t.get(None)
        self.assertEqual((null_group.count, null_group.sum), (1, 10))
        self.assertEqual(len(t.result()), 3)


class TestExactIntegers(unittest.TestCase):
    def test_big_integer_sum_is_exact(self):
        t = make_table(key_type=STR, value_type=INT)
        big = 10**30
        t.insert_batch(Batch(
            [("id", INT), ("key", STR), ("value", INT)],
            [(1, "a", big), (2, "a", big), (3, "a", 7)],
        ))
        a = t.get("a")
        self.assertEqual(a.sum, 2 * big + 7)
        self.assertIsInstance(a.sum, int)
        self.assertEqual(a.min, 7)

    def test_int_column_rejects_float_and_bool(self):
        with self.assertRaises(TypeError):
            Batch([("id", INT), ("key", STR), ("value", INT)], [(1, "a", 1.5)])
        with self.assertRaises(TypeError):
            Batch([("id", INT), ("key", STR), ("value", INT)], [(1, "a", True)])

    def test_float_column_coerces_int_to_float(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", 3)]))
        a = t.get("a")
        self.assertEqual(a.sum, 3.0)
        self.assertIsInstance(a.sum, float)


class TestUpdateMigration(unittest.TestCase):
    def test_cross_group_update_is_atomic_no_double_count(self):
        t = make_table()
        t.insert_batch(row_batch([
            (1, "a", 10.0),
            (2, "a", 20.0),
            (3, "b", 5.0),
        ]))
        # Move row 1 from group a to group b.
        t.update_batch(row_batch([(1, "b", 10.0)]))
        a, b = t.get("a"), t.get("b")
        self.assertEqual((a.count, a.sum, a.min), (1, 20.0, 20.0))
        self.assertEqual((b.count, b.sum, b.min), (2, 15.0, 5.0))
        self.assertEqual(sum(r.count for r in t.result()), 3)  # no double count
        self.assertEqual(len(t), 3)

    def test_update_same_group_value(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", 1.0), (2, "a", 9.0)]))
        t.update_batch(row_batch([(1, "a", 100.0)]))
        a = t.get("a")
        self.assertEqual((a.count, a.sum, a.min), (2, 109.0, 9.0))

    def test_update_to_null_key_and_null_value(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", 1.0)]))
        t.update_batch(row_batch([(1, None, None)]))
        self.assertIsNone(t.get("a"))  # group a reclaimed
        null_group = t.get(None)
        self.assertEqual(null_group.count, 1)
        self.assertIsNone(null_group.sum)


class TestDeleteAndMin(unittest.TestCase):
    def test_delete_min_finds_next_min(self):
        t = make_table()
        t.insert_batch(row_batch([
            (1, "a", 1.0), (2, "a", 3.0), (3, "a", 2.0), (4, "a", 1.0),
        ]))
        self.assertEqual(t.get("a").min, 1.0)
        t.delete_batch(Batch([("id", INT)], [(1,)]))
        self.assertEqual(t.get("a").min, 1.0)   # duplicate min still present
        t.delete_batch(Batch([("id", INT)], [(4,)]))
        self.assertEqual(t.get("a").min, 2.0)   # next min found
        t.delete_batch(Batch([("id", INT)], [(3,)]))
        self.assertEqual(t.get("a").min, 3.0)
        self.assertEqual(t.get("a").sum, 3.0)

    def test_empty_group_reclaimed(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", 1.0), (2, "b", 2.0)]))
        t.delete_batch(Batch([("id", INT)], [(1,)]))
        self.assertIsNone(t.get("a"))
        self.assertNotIn("a", t)
        self.assertEqual([r.key for r in t.result()], ["b"])
        # Re-insert into reclaimed group works.
        t.insert_batch(row_batch([(9, "a", 5.0)]))
        self.assertEqual((t.get("a").count, t.get("a").sum), (1, 5.0))

    def test_delete_last_null_row_reclaims_group(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", None)]))
        t.delete_batch(Batch([("id", INT)], [(1,)]))
        self.assertEqual(t.result(), [])


class TestAtomicityAndErrors(unittest.TestCase):
    def test_failing_batch_changes_nothing(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", 1.0), (2, "a", 2.0)]))
        before = t.result()
        bad = Batch(
            [("op", STR), ("id", INT), ("key", STR), ("value", FLOAT)],
            [
                ("U", 1, "b", 10.0),   # valid
                ("I", 3, "c", 30.0),   # valid
                ("D", 99, None, None), # invalid: unknown id
            ],
        )
        with self.assertRaises(KeyError):
            t.apply_batch(bad)
        self.assertEqual(t.result(), before)  # fully rolled back
        self.assertEqual(len(t), 2)

    def test_duplicate_insert_id_rejected(self):
        t = make_table()
        t.insert_batch(row_batch([(1, "a", 1.0)]))
        with self.assertRaises(KeyError):
            t.insert_batch(row_batch([(1, "b", 2.0)]))
        # ... including duplicates within one batch
        with self.assertRaises(KeyError):
            t.insert_batch(row_batch([(5, "a", 1.0), (5, "a", 2.0)]))

    def test_update_or_delete_unknown_id_rejected(self):
        t = make_table()
        with self.assertRaises(KeyError):
            t.update_batch(row_batch([(1, "a", 1.0)]))
        with self.assertRaises(KeyError):
            t.delete_batch(Batch([("id", INT)], [(1,)]))

    def test_schema_mismatch_rejected(self):
        t = make_table()
        with self.assertRaises(ValueError):
            t.insert_batch(Batch([("id", INT), ("key", STR)], [(1, "a")]))
        with self.assertRaises(ValueError):
            t.insert_batch(Batch(
                [("id", INT), ("key", STR), ("value", INT)], [(1, "a", 1)]
            ))

    def test_wrong_cell_type_rejected(self):
        with self.assertRaises(TypeError):
            Batch([("id", INT), ("key", STR), ("value", FLOAT)], [(1, "a", "x")])
        with self.assertRaises(ValueError):
            Batch([("id", INT), ("key", STR)], [(1, "a", "extra")])


class TestMixedBatch(unittest.TestCase):
    def test_apply_mixed_ops_atomically(self):
        t = make_table()
        t.apply_batch(Batch(
            [("op", STR), ("id", INT), ("key", STR), ("value", FLOAT)],
            [
                ("I", 1, "a", 1.0),
                ("I", 2, "a", 2.0),
                ("I", 3, "b", 10.0),
            ],
        ))
        t.apply_batch(Batch(
            [("op", STR), ("id", INT), ("key", STR), ("value", FLOAT)],
            [
                ("U", 3, "a", 10.0),   # migrate b -> a
                ("D", 1, None, None),  # delete min of a
                ("I", 4, "b", 0.5),    # new row in b
            ],
        ))
        a, b = t.get("a"), t.get("b")
        self.assertEqual((a.count, a.sum, a.min), (2, 12.0, 2.0))
        self.assertEqual((b.count, b.sum, b.min), (1, 0.5, 0.5))
        self.assertEqual(len(t), 3)


class TestRandomizedAgainstReference(unittest.TestCase):
    """Differential test: random op streams vs a naive recomputation."""

    def test_random_ops_match_reference(self):
        import random

        rng = random.Random(20260924)
        keys = ["a", "b", "c", None]
        values = [None, 1, 2, 3, 10**25, -7]
        t = make_table(key_type=STR, value_type=INT)
        schema = [("id", INT), ("key", STR), ("value", INT)]
        live = {}  # id -> (key, value)
        next_id = 1
        for _ in range(300):
            kind = rng.choice(["I", "I", "U", "D"])
            if kind == "I" or not live:
                rows = [(next_id, rng.choice(keys), rng.choice(values))]
                t.insert_batch(Batch(schema, rows))
                live[next_id] = rows[0][1:]
                next_id += 1
            elif kind == "U":
                rid = rng.choice(list(live))
                key, value = rng.choice(keys), rng.choice(values)
                t.update_batch(Batch(schema, [(rid, key, value)]))
                live[rid] = (key, value)
            else:
                rid = rng.choice(list(live))
                t.delete_batch(Batch([("id", INT)], [(rid,)]))
                del live[rid]

            # Reference: recompute from scratch.
            ref = {}
            for key, value in live.values():
                ref.setdefault(key, []).append(value)
            expected = {}
            for key, vals in ref.items():
                nn = [v for v in vals if v is not None]
                expected[key] = (
                    len(vals),
                    sum(nn) if nn else None,
                    min(nn) if nn else None,
                )
            got = {r.key: (r.count, r.sum, r.min) for r in t.result()}
            self.assertEqual(got, expected)
            self.assertEqual(len(t), len(live))


if __name__ == "__main__":
    unittest.main(verbosity=2)
