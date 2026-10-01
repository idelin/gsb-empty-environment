import random
import unittest
from collections import Counter

from equivstats import Dataset, NullPolicy, Record, merge_shards, recount, verify_shard_merge


class TestRecount(unittest.TestCase):
    def test_duplicates_and_nulls_recounted_independently(self):
        records = [Record(("q",), "x") for _ in range(10)]
        records += [Record(("q",), None) for _ in range(3)]
        counts = recount(records)
        self.assertEqual(counts[("q",)], Counter({"x": 10, None: 3}))


class TestShardMerge(unittest.TestCase):
    def test_shard_merge_equals_direct_recount(self):
        rng = random.Random(7)
        records = [
            Record((f"q{rng.randint(0, 3)}",), rng.choice(["a", "b", "c", None]))
            for _ in range(60)
        ]
        shards = [records[0:17], records[17:41], records[41:]]
        self.assertTrue(verify_shard_merge(shards))
        merged = merge_shards(shards)
        self.assertEqual(merged, recount(records))
        # 合并后 null 与重复值保持原样
        self.assertEqual(sum(c.get(None, 0) for c in merged.values()),
                         sum(1 for r in records if r.sensitive is None))

    def test_empty_shards(self):
        self.assertTrue(verify_shard_merge([[], [], []]))
        self.assertEqual(merge_shards([[]]), {})


class TestDatasetIntegration(unittest.TestCase):
    def test_dataset_counts_match_independent_recount(self):
        rng = random.Random(11)
        dataset = Dataset(null_policy=NullPolicy.AS_CATEGORY, log_base=10)
        records = [
            Record((f"q{rng.randint(0, 2)}",), rng.choice(["a", "b", None]))
            for _ in range(30)
        ]
        dataset.add_many(records)
        for record in rng.sample(records, 8):
            dataset.remove(record)
        self.assertEqual(dataset.class_counts(), recount(dataset.records))


if __name__ == "__main__":
    unittest.main()
