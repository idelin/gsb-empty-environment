import math
import unittest

from grouped_agg import Batch, ColType, GroupedAggregate, GroupRow, UNSET


def make_agg(key_type=ColType.STR, value_type=ColType.INT):
    return GroupedAggregate(key_type, value_type)


class TestBasicAggregation(unittest.TestCase):
    def test_insert_and_aggregate(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.insert(2, "A", 20)
        agg.insert(3, "B", 5)
        self.assertEqual(agg.group("A"), GroupRow("A", count=2, sum=30, min=10))
        self.assertEqual(agg.group("B"), GroupRow("B", count=1, sum=5, min=5))

    def test_insert_typed_batch(self):
        agg = make_agg()
        batch = Batch.from_rows(
            {"id": ColType.INT, "key": ColType.STR, "value": ColType.INT},
            [(1, "A", 10), (2, "A", 20), (3, "B", 7)],
        )
        agg.insert_batch(batch)
        self.assertEqual(len(agg), 3)
        self.assertEqual(agg.group("A").sum, 30)

    def test_batch_rejects_wrong_value_type(self):
        with self.assertRaises(TypeError):
            Batch.from_rows(
                {"id": ColType.INT, "key": ColType.STR, "value": ColType.INT},
                [(1, "A", 1.5)],
            )

    def test_batch_rejects_mismatched_column_type(self):
        agg = make_agg()
        batch = Batch.from_rows(
            {"id": ColType.INT, "key": ColType.STR, "value": ColType.FLOAT},
            [(1, "A", 1.5)],
        )
        with self.assertRaises(TypeError):
            agg.insert_batch(batch)

    def test_result_sorted_null_key_last(self):
        agg = make_agg()
        agg.insert(1, None, 1)
        agg.insert(2, "B", 2)
        agg.insert(3, "A", 3)
        self.assertEqual([r.key for r in agg.result()], ["A", "B", None])


class TestUpdatesAndDeletes(unittest.TestCase):
    def test_update_value_same_group(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.insert(2, "A", 20)
        agg.update(1, value=99)
        self.assertEqual(agg.group("A"), GroupRow("A", count=2, sum=119, min=20))

    def test_cross_group_migration_is_atomic_no_double_count(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.insert(2, "A", 20)
        agg.insert(3, "B", 5)
        agg.update(1, key="B")  # same id moves A -> B
        self.assertEqual(len(agg), 3)
        self.assertEqual(agg.group("A"), GroupRow("A", count=1, sum=20, min=20))
        self.assertEqual(agg.group("B"), GroupRow("B", count=2, sum=15, min=5))

    def test_update_key_and_value_together(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.update(1, key="B", value=42)
        self.assertIsNone(agg.group("A"))
        self.assertEqual(agg.group("B"), GroupRow("B", count=1, sum=42, min=42))

    def test_update_with_unset_keeps_old_field(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.update(1)  # no-op
        self.assertEqual(agg.group("A"), GroupRow("A", count=1, sum=10, min=10))

    def test_update_can_set_null_value(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.insert(2, "A", 20)
        agg.update(1, value=None)  # explicit NULL, not "unchanged"
        self.assertEqual(agg.group("A"), GroupRow("A", count=2, sum=20, min=20))

    def test_delete_row(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.insert(2, "A", 20)
        agg.delete(1)
        self.assertEqual(agg.group("A"), GroupRow("A", count=1, sum=20, min=20))
        self.assertNotIn(1, agg)

    def test_delete_min_exposes_next_min(self):
        agg = make_agg()
        for i, v in enumerate([3, 1, 2, 1], start=1):
            agg.insert(i, "A", v)
        self.assertEqual(agg.group("A").min, 1)
        agg.delete(2)  # remove one of the two minimums
        self.assertEqual(agg.group("A").min, 1)
        agg.delete(4)  # remove the other minimum
        self.assertEqual(agg.group("A").min, 2)
        agg.delete(3)
        self.assertEqual(agg.group("A").min, 3)

    def test_duplicate_id_rejected(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        with self.assertRaises(KeyError):
            agg.insert(1, "B", 20)

    def test_update_or_delete_unknown_id_rejected(self):
        agg = make_agg()
        with self.assertRaises(KeyError):
            agg.update(99, value=1)
        with self.assertRaises(KeyError):
            agg.delete(99)


class TestNullsAndNaN(unittest.TestCase):
    def test_null_key_group(self):
        agg = make_agg()
        agg.insert(1, None, 10)
        agg.insert(2, None, 20)
        agg.insert(3, "A", 1)
        self.assertEqual(agg.group(None), GroupRow(None, count=2, sum=30, min=10))

    def test_update_to_null_key_migrates_group(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.update(1, key=None)
        self.assertIsNone(agg.group("A"))
        self.assertEqual(agg.group(None), GroupRow(None, count=1, sum=10, min=10))

    def test_all_null_group_sum_is_null_but_count_counts_rows(self):
        agg = make_agg()
        agg.insert(1, "A", None)
        agg.insert(2, "A", None)
        row = agg.group("A")
        self.assertEqual(row.count, 2)      # count(*) still counts the rows
        self.assertIsNone(row.sum)          # sum of all-NULL group is NULL
        self.assertIsNone(row.min)

    def test_nulls_mixed_with_values(self):
        agg = make_agg()
        agg.insert(1, "A", None)
        agg.insert(2, "A", 7)
        self.assertEqual(agg.group("A"), GroupRow("A", count=2, sum=7, min=7))

    def test_nan_is_not_null(self):
        agg = make_agg(ColType.STR, ColType.FLOAT)
        agg.insert(1, "A", float("nan"))
        agg.insert(2, "A", 2.0)
        row = agg.group("A")
        self.assertEqual(row.count, 2)              # NaN row is counted
        self.assertTrue(math.isnan(row.sum))        # NaN propagates through sum
        self.assertEqual(row.min, 2.0)              # min skips NaN when possible
        agg.delete(2)
        row = agg.group("A")
        self.assertTrue(math.isnan(row.min))        # only NaN left -> min is NaN
        self.assertIsNotNone(row.min)               # ...which is not NULL

    def test_nan_group_recovers_after_nan_removed(self):
        agg = make_agg(ColType.STR, ColType.FLOAT)
        agg.insert(1, "A", float("nan"))
        agg.insert(2, "A", 3.0)
        agg.delete(1)
        self.assertEqual(agg.group("A"), GroupRow("A", count=1, sum=3.0, min=3.0))


class TestEmptyGroupReclamation(unittest.TestCase):
    def test_group_reclaimed_when_last_row_deleted(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.delete(1)
        self.assertIsNone(agg.group("A"))
        self.assertEqual(agg.group_count, 0)
        self.assertEqual(agg.result(), [])

    def test_group_reclaimed_when_last_row_migrates(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.insert(2, "B", 20)
        agg.update(1, key="B")
        self.assertIsNone(agg.group("A"))
        self.assertEqual(agg.group_count, 1)

    def test_null_key_group_reclaimed(self):
        agg = make_agg()
        agg.insert(1, None, 10)
        agg.delete(1)
        self.assertIsNone(agg.group(None))
        self.assertEqual(agg.group_count, 0)

    def test_group_can_be_recreated_after_reclaim(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.delete(1)
        agg.insert(2, "A", 5)
        self.assertEqual(agg.group("A"), GroupRow("A", count=1, sum=5, min=5))


class TestAtomicity(unittest.TestCase):
    def test_failed_batch_leaves_state_unchanged(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        before = agg.result()
        with self.assertRaises(TypeError):
            agg.apply_ops([
                ("insert", 2, "A", 20),
                ("insert", 3, "A", "not-an-int"),  # invalid -> whole batch aborts
            ])
        self.assertEqual(agg.result(), before)
        self.assertEqual(len(agg), 1)

    def test_failed_batch_with_unknown_id_leaves_state_unchanged(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        with self.assertRaises(KeyError):
            agg.apply_ops([
                ("update", 1, "B", UNSET),
                ("delete", 999),
            ])
        self.assertEqual(agg.group("A"), GroupRow("A", count=1, sum=10, min=10))
        self.assertIsNone(agg.group("B"))

    def test_multi_op_batch_applies_atomically(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        agg.insert(2, "A", 20)
        agg.apply_ops([
            ("update", 1, "B", UNSET),   # move 1 -> B
            ("delete", 2),
            ("insert", 3, "B", 5),
        ])
        self.assertIsNone(agg.group("A"))
        self.assertEqual(agg.group("B"), GroupRow("B", count=2, sum=15, min=5))

    def test_insert_then_delete_same_id_in_one_batch(self):
        agg = make_agg()
        agg.apply_ops([
            ("insert", 1, "A", 10),
            ("delete", 1),
        ])
        self.assertEqual(len(agg), 0)
        self.assertEqual(agg.group_count, 0)


class TestExactIntegers(unittest.TestCase):
    def test_big_integer_sum_is_exact(self):
        agg = make_agg()
        big1 = 10**30 + 12345
        big2 = 10**30 + 67890
        agg.insert(1, "A", big1)
        agg.insert(2, "A", big2)
        self.assertEqual(agg.group("A").sum, 2 * 10**30 + 80235)
        agg.delete(1)
        self.assertEqual(agg.group("A").sum, big2)

    def test_negative_and_mixed_ints(self):
        agg = make_agg()
        for i, v in enumerate([-5, 3, -2], start=1):
            agg.insert(i, "A", v)
        self.assertEqual(agg.group("A"), GroupRow("A", count=3, sum=-4, min=-5))


class TestTypeValidation(unittest.TestCase):
    def test_bool_is_not_an_int(self):
        agg = make_agg()
        with self.assertRaises(TypeError):
            agg.insert(1, "A", True)

    def test_wrong_key_type_rejected(self):
        agg = make_agg()
        with self.assertRaises(TypeError):
            agg.insert(1, 123, 10)

    def test_wrong_value_type_rejected_on_update(self):
        agg = make_agg()
        agg.insert(1, "A", 10)
        with self.assertRaises(TypeError):
            agg.update(1, value="nope")
        self.assertEqual(agg.group("A"), GroupRow("A", count=1, sum=10, min=10))

    def test_float_column_accepts_int_and_float(self):
        agg = make_agg(ColType.STR, ColType.FLOAT)
        agg.insert(1, "A", 1)
        agg.insert(2, "A", 2.5)
        self.assertEqual(agg.group("A").sum, 3.5)

    def test_string_values(self):
        agg = make_agg(ColType.STR, ColType.STR)
        agg.insert(1, "A", "pear")
        agg.insert(2, "A", "apple")
        row = agg.group("A")
        self.assertEqual(row.count, 2)
        self.assertEqual(row.sum, "pearapple")
        self.assertEqual(row.min, "apple")


if __name__ == "__main__":
    unittest.main()
