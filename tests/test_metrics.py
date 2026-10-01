import math
import unittest

from hierarchy import Hierarchy
from intervals import Decision
from metrics import (
    ClassStats,
    Config,
    NullPolicy,
    Status,
    entropy_of_counts,
    recompute_stats,
)


def make_config(**kwargs):
    defaults = dict(distinct_threshold=2, entropy_threshold=0.5, log_base=2.0)
    defaults.update(kwargs)
    return Config(**defaults)


class TestDistinctAndEntropy(unittest.TestCase):
    def test_same_value_ten_times_counts_as_one(self):
        stats = recompute_stats(["flu"] * 10, make_config())
        self.assertEqual(stats.distinct_count(), 1)
        self.assertEqual(stats.n, 10)
        self.assertEqual(stats.entropy(), 0.0)

    def test_uniform_entropy_equals_log_of_k(self):
        stats = recompute_stats(["a", "b", "c", "d"] * 5, make_config())
        self.assertAlmostEqual(stats.entropy(), 2.0, places=12)

    def test_log_base_conversion(self):
        counts = {"a": 3, "b": 1, "c": 4}
        h2 = entropy_of_counts(counts, 2.0)
        h10 = entropy_of_counts(counts, 10.0)
        he = entropy_of_counts(counts, math.e)
        self.assertAlmostEqual(h10, h2 / math.log2(10.0), places=12)
        self.assertAlmostEqual(he, h2 * math.log(2.0), places=12)

    def test_contributions_sum_to_entropy(self):
        stats = recompute_stats(["a", "a", "b", "c", "c", "c"], make_config())
        total = sum(c.entropy_term for c in stats.contributions().values())
        self.assertAlmostEqual(total, stats.entropy(), places=12)
        shares = sum(c.share for c in stats.contributions().values())
        self.assertAlmostEqual(shares, 1.0, places=12)


class TestNullPolicy(unittest.TestCase):
    def test_ignore_policy_drops_nulls(self):
        config = make_config(null_policy=NullPolicy.IGNORE)
        stats = recompute_stats(["a", None, "b", None], config)
        self.assertEqual(stats.n, 2)
        self.assertEqual(stats.distinct_count(), 2)

    def test_count_as_category_policy(self):
        config = make_config(null_policy=NullPolicy.COUNT_AS_CATEGORY)
        stats = recompute_stats(["a", None, "b", None], config)
        self.assertEqual(stats.n, 4)
        self.assertEqual(stats.distinct_count(), 3)
        self.assertIn(None, stats.counts)

    def test_all_null_class_skipped_by_config(self):
        config = make_config(null_policy=NullPolicy.IGNORE, count_all_null_class=False)
        stats = recompute_stats([None, None, None], config)
        self.assertIs(stats.evaluate().status, Status.SKIPPED)

    def test_all_null_class_evaluated_by_config(self):
        config = make_config(null_policy=NullPolicy.IGNORE, count_all_null_class=True)
        stats = recompute_stats([None, None, None], config)
        report = stats.evaluate()
        self.assertIs(report.status, Status.FAIL)
        self.assertIn("distinct", report.failed_metrics)


class TestEvaluate(unittest.TestCase):
    def test_failed_class_reports_metrics_and_contributions(self):
        config = make_config(distinct_threshold=3, entropy_threshold=1.5)
        stats = recompute_stats(["a", "a", "a", "b"], config, class_key="K")
        report = stats.evaluate()
        self.assertIs(report.status, Status.FAIL)
        self.assertEqual(set(report.failed_metrics), {"distinct", "entropy"})
        self.assertEqual(set(report.contributions), {"a", "b"})

    def test_entropy_undecided_status(self):
        config = make_config(distinct_threshold=1, entropy_threshold=1.0)
        stats = recompute_stats(["a"] * 50 + ["b"] * 50, config)
        report = stats.evaluate()
        self.assertIs(report.entropy_decision, Decision.UNDECIDED)
        self.assertIs(report.status, Status.UNDECIDED)

    def test_pass_status(self):
        config = make_config(distinct_threshold=2, entropy_threshold=0.5)
        stats = recompute_stats(["a", "b", "c"], config)
        self.assertIs(stats.evaluate().status, Status.PASS)


class TestIncrementalDeleteAndVerify(unittest.TestCase):
    def test_incremental_delete_matches_recompute(self):
        config = make_config(null_policy=NullPolicy.COUNT_AS_CATEGORY)
        records = ["a", "b", "a", None, "c", "a", "b", None, "c", "c"]
        stats = recompute_stats(records, config)
        removed = [records[0], records[3], records[9]]
        remaining = [r for i, r in enumerate(records) if i not in (0, 3, 9)]
        for value in removed:
            stats.delete(value)
        self.assertTrue(stats.verify(remaining))
        self.assertEqual(stats.n, len(remaining))

    def test_delete_missing_value_raises(self):
        stats = recompute_stats(["a"], make_config())
        with self.assertRaises(KeyError):
            stats.delete("b")

    def test_delete_ignored_null_is_noop(self):
        config = make_config(null_policy=NullPolicy.IGNORE)
        stats = recompute_stats(["a", None], config)
        self.assertFalse(stats.delete(None))
        self.assertEqual(stats.n, 1)


class TestShardMerge(unittest.TestCase):
    def test_merged_shards_match_whole_recompute(self):
        config = make_config(null_policy=NullPolicy.COUNT_AS_CATEGORY)
        records = ["a", None, "b", "a", "c", None, "b", "a", "c", "d", "a"]
        shards = [records[0::2], records[1::2]]
        merged = ClassStats(config)
        for shard in shards:
            merged.merge_shard(recompute_stats(shard, config))
        baseline = recompute_stats(records, config)
        self.assertEqual(dict(merged.counts), dict(baseline.counts))
        self.assertAlmostEqual(merged.entropy(), baseline.entropy(), places=12)
        self.assertEqual(merged.n, baseline.n)


class TestHierarchyMergeRollback(unittest.TestCase):
    def test_merge_and_rollback_restores_contributions(self):
        config = make_config(null_policy=NullPolicy.COUNT_AS_CATEGORY)
        records = ["flu", "flu", "cold", "diabetes", "asthma", None]
        stats = recompute_stats(records, config)
        before_counts = dict(stats.counts)
        before_contribs = stats.contributions()
        hierarchy = Hierarchy(
            {"flu": "respiratory", "cold": "respiratory", "asthma": "respiratory"}
        )
        mapping = hierarchy.merge_mapping(stats.counts)
        self.assertTrue(stats.merge_values(mapping))
        self.assertEqual(stats.counts["respiratory"], 4)
        self.assertEqual(stats.distinct_count(), 3)
        self.assertEqual(stats.n, len(records))
        restored = stats.rollback()
        self.assertEqual(dict(stats.counts), before_counts)
        self.assertEqual(set(restored), set(before_contribs))
        for value, contrib in restored.items():
            self.assertEqual(contrib.count, before_contribs[value].count)

    def test_merge_is_noop_without_matching_values(self):
        stats = recompute_stats(["x", "y"], make_config())
        self.assertFalse(stats.merge_values({"flu": "respiratory"}))


if __name__ == "__main__":
    unittest.main()
