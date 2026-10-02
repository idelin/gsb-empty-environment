import unittest

from bootstrap_ci import (
    IIDPlan,
    MovingBlockPlan,
    PairedPlan,
    RandomSource,
    StratifiedPlan,
    UnsupportedMethodError,
)


class TestIIDPlan(unittest.TestCase):
    def test_resample_size_and_membership(self):
        plan = IIDPlan([1, 2, 3, 4])
        rng = RandomSource(0).stream(0)
        sample = plan.resample(rng)
        self.assertEqual(len(sample), 4)
        self.assertTrue(set(sample) <= {1, 2, 3, 4})

    def test_jackknife_leaves_one_out(self):
        plan = IIDPlan([10, 20, 30])
        payloads = plan.jackknife()
        self.assertEqual(payloads, [[20, 30], [10, 30], [10, 20]])

    def test_needs_two_observations(self):
        with self.assertRaises(ValueError):
            IIDPlan([1])


class TestPairedPlan(unittest.TestCase):
    def test_pairs_are_resampled_jointly(self):
        # Component 2 is a deterministic function of component 1; any split
        # of a pair would break the invariant b == 10 * a.
        pairs = [(i, 10 * i) for i in range(8)]
        plan = PairedPlan(pairs)
        for rep in range(50):
            sample = plan.resample(RandomSource(99).stream(rep))
            self.assertEqual(len(sample), 8)
            for a, b in sample:
                self.assertEqual(b, 10 * a)

    def test_jackknife_removes_whole_pairs(self):
        plan = PairedPlan([(1, 2), (3, 4), (5, 6)])
        self.assertEqual(plan.jackknife(),
                         [[(3, 4), (5, 6)], [(1, 2), (5, 6)], [(1, 2), (3, 4)]])

    def test_rejects_malformed_pairs(self):
        with self.assertRaises(ValueError):
            PairedPlan([(1, 2, 3), (4, 5)])


class TestStratifiedPlan(unittest.TestCase):
    def test_stratum_sizes_preserved_and_no_crossing(self):
        plan = StratifiedPlan({"a": [1, 2, 3], "b": [10, 20, 30, 40]})
        for rep in range(30):
            sample = plan.resample(RandomSource(5).stream(rep))
            self.assertEqual(len(sample["a"]), 3)
            self.assertEqual(len(sample["b"]), 4)
            self.assertTrue(set(sample["a"]) <= {1, 2, 3})
            self.assertTrue(set(sample["b"]) <= {10, 20, 30, 40})

    def test_jackknife_spans_all_units(self):
        plan = StratifiedPlan({"a": [1, 2], "b": [3, 4]})
        payloads = plan.jackknife()
        self.assertEqual(len(payloads), 4)
        for payload in payloads:
            self.assertEqual(sum(len(v) for v in payload.values()), 3)

    def test_stratum_needs_two_observations(self):
        with self.assertRaises(ValueError):
            StratifiedPlan({"a": [1]})


class TestMovingBlockPlan(unittest.TestCase):
    def test_output_length_and_membership(self):
        series = list(range(10))
        for boundary in ("circular", "truncate"):
            plan = MovingBlockPlan(series, block_length=3, boundary=boundary)
            for rep in range(20):
                sample = plan.resample(RandomSource(3).stream(rep))
                self.assertEqual(len(sample), 10)
                self.assertTrue(set(sample) <= set(series))

    def test_circular_boundary_wraps(self):
        # With block_length == n and circular boundary, a resample is a
        # rotation of the series; wrap-around must occur for some replicate.
        series = list(range(6))
        plan = MovingBlockPlan(series, block_length=6, boundary="circular")
        saw_wrap = False
        for rep in range(40):
            sample = plan.resample(RandomSource(11).stream(rep))
            if sample[0] != 0 and 0 in sample:
                saw_wrap = True
        self.assertTrue(saw_wrap)

    def test_truncate_boundary_never_wraps(self):
        series = list(range(6))
        plan = MovingBlockPlan(series, block_length=6, boundary="truncate")
        for rep in range(40):
            sample = plan.resample(RandomSource(13).stream(rep))
            self.assertEqual(sample, series)  # only start 0 is legal

    def test_block_length_one_is_iid(self):
        series = [5, 6, 7, 8]
        plan = MovingBlockPlan(series, block_length=1)
        sample = plan.resample(RandomSource(1).stream(0))
        self.assertEqual(len(sample), 4)
        self.assertTrue(set(sample) <= set(series))

    def test_invalid_configuration(self):
        with self.assertRaises(ValueError):
            MovingBlockPlan([1, 2], block_length=3)
        with self.assertRaises(ValueError):
            MovingBlockPlan([1, 2], block_length=0)
        with self.assertRaises(ValueError):
            MovingBlockPlan([1, 2], block_length=1, boundary="sideways")

    def test_jackknife_fails_explicitly(self):
        plan = MovingBlockPlan([1, 2, 3], block_length=2)
        with self.assertRaises(UnsupportedMethodError):
            plan.jackknife()


if __name__ == "__main__":
    unittest.main()
