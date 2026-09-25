import unittest

from bucketindex.bucketing import BucketRule, FineBuckets, RuleRegistry


class TestFineBuckets(unittest.TestCase):
    def setUp(self):
        self.fine = FineBuckets(origin=0, width=10)

    def test_negative_times_floor_down(self):
        # -1 falls in [-10, 0), not in [0, 10).
        self.assertEqual(self.fine.index(-1), -1)
        self.assertEqual(self.fine.bounds(-1), (-10, 0))
        self.assertEqual(self.fine.index(-10), -1)
        self.assertEqual(self.fine.index(-11), -2)

    def test_non_negative_indices(self):
        self.assertEqual(self.fine.index(0), 0)
        self.assertEqual(self.fine.index(9), 0)
        self.assertEqual(self.fine.index(10), 1)

    def test_non_zero_origin(self):
        fine = FineBuckets(origin=5, width=10)
        self.assertEqual(fine.index(5), 0)
        self.assertEqual(fine.index(4), -1)
        self.assertEqual(fine.bounds(-1), (-5, 5))


class TestBucketRule(unittest.TestCase):
    def setUp(self):
        self.fine = FineBuckets(origin=0, width=10)

    def test_non_uniform_business_buckets(self):
        rule = BucketRule(0, self.fine, (-20, 5 * 10, 12 * 10))
        # 4 business buckets: (-inf,-20), [-20,50), [50,120), [120,inf)
        self.assertEqual(rule.bucket_count, 4)
        self.assertEqual(rule.locate(-21), 0)
        self.assertEqual(rule.locate(-20), 1)
        self.assertEqual(rule.locate(119), 2)
        self.assertEqual(rule.locate(120), 3)
        ranges = rule.fine_ranges()
        self.assertEqual(ranges[1], (-2, 5))
        self.assertEqual(ranges[2], (5, 12))

    def test_misaligned_boundary_rejected(self):
        with self.assertRaises(ValueError):
            BucketRule(0, self.fine, (15,))

    def test_registry_keeps_all_versions(self):
        reg = RuleRegistry(self.fine)
        r0 = reg.get(0)  # default: single open bucket
        self.assertEqual(r0.bucket_count, 1)
        r1 = reg.register((0,))
        r2 = reg.register((-50, 0, 50))
        self.assertEqual((r1.version, r2.version), (1, 2))
        self.assertIs(reg.get(1), r1)
        self.assertIs(reg.current, r2)
        # Old versions remain intact and queryable.
        self.assertEqual(reg.get(1).boundaries, (0,))


if __name__ == "__main__":
    unittest.main()
