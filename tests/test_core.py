import unittest
from collections import Counter

from equivstats import Dataset, Hierarchy, NullPolicy, Record, verify_incremental


def make_records():
    return [
        Record(("30s", "100"), "flu"),
        Record(("30s", "100"), "flu"),
        Record(("30s", "100"), "covid"),
        Record(("30s", "100"), None),
        Record(("40s", "200"), "cold"),
        Record(("40s", "200"), "asthma"),
    ]


class TestIncremental(unittest.TestCase):
    def test_add_then_incremental_delete_matches_recount(self):
        dataset = Dataset()
        dataset.add_many(make_records())
        self.assertTrue(verify_incremental(dataset))
        dataset.remove(Record(("30s", "100"), "flu"))
        dataset.remove(Record(("30s", "100"), None))
        dataset.remove(Record(("40s", "200"), "cold"))
        self.assertTrue(verify_incremental(dataset))
        counts = dataset.class_counts()
        self.assertEqual(counts[("30s", "100")], Counter({"flu": 1, "covid": 1}))
        self.assertEqual(counts[("40s", "200")], Counter({"asthma": 1}))

    def test_removing_last_record_drops_class(self):
        dataset = Dataset()
        record = Record(("q",), "a")
        dataset.add(record)
        dataset.remove(record)
        self.assertEqual(dataset.class_counts(), {})
        self.assertTrue(verify_incremental(dataset))

    def test_remove_absent_record_raises(self):
        dataset = Dataset()
        with self.assertRaises(ValueError):
            dataset.remove(Record(("q",), "a"))


class TestGeneralizationRollback(unittest.TestCase):
    def test_hierarchy_levels(self):
        hierarchy = Hierarchy({"flu": "resp", "covid": "resp", "resp": "disease"})
        mapping1 = hierarchy.mapping_at_level(["flu", "covid", "cold"], 1)
        self.assertEqual(mapping1, {"flu": "resp", "covid": "resp", "cold": "cold"})
        mapping2 = hierarchy.mapping_at_level(["flu"], 2)
        self.assertEqual(mapping2, {"flu": "disease"})

    def test_generalize_keeps_original_contributions_for_rollback(self):
        dataset = Dataset()
        dataset.add_many(make_records())
        original = dataset.class_counts()
        dataset.generalize({"flu": "resp", "covid": "resp"})
        merged = dataset.class_counts()
        self.assertEqual(merged[("30s", "100")], Counter({"resp": 3, None: 1}))
        dataset.rollback()
        self.assertEqual(dataset.class_counts(), original)
        self.assertTrue(verify_incremental(dataset))

    def test_rollback_without_generalize_raises(self):
        dataset = Dataset()
        with self.assertRaises(IndexError):
            dataset.rollback()

    def test_layered_generalize_and_rollback(self):
        dataset = Dataset()
        dataset.add_many(make_records())
        original = dataset.class_counts()
        dataset.generalize({"flu": "resp", "covid": "resp", "cold": "resp"})
        dataset.generalize({"resp": "disease", "asthma": "disease"})
        self.assertEqual(dataset.class_counts()[("30s", "100")],
                         Counter({"disease": 3, None: 1}))
        dataset.rollback()
        self.assertEqual(dataset.class_counts()[("30s", "100")],
                         Counter({"resp": 3, None: 1}))
        dataset.rollback()
        self.assertEqual(dataset.class_counts(), original)


if __name__ == "__main__":
    unittest.main()
