import random
import unittest

from bootci.resample import (
    resample_mbb,
    resample_one,
    resample_paired,
    resample_stratified,
    resample_two,
)
from bootci.samples import (
    OneSample,
    PairedSample,
    StratifiedSample,
    TimeSeriesSample,
    TwoSample,
)


class TestResampleOne(unittest.TestCase):
    def test_size_and_support(self):
        sample = OneSample([1.0, 2.0, 3.0])
        out = resample_one(sample, random.Random(0))
        self.assertEqual(len(out), 3)
        self.assertTrue(set(out) <= set(sample.values))


class TestResampleTwo(unittest.TestCase):
    def test_group_sizes_fixed_no_leak(self):
        sample = TwoSample([1.0, 2.0], [100.0, 200.0, 300.0])
        rx, ry = resample_two(sample, random.Random(0))
        self.assertEqual(len(rx), 2)
        self.assertEqual(len(ry), 3)
        self.assertTrue(set(rx) <= {1.0, 2.0})
        self.assertTrue(set(ry) <= {100.0, 200.0, 300.0})


class TestResamplePaired(unittest.TestCase):
    def test_pairs_kept_together(self):
        # y = x + 100：若成对抽取，重采样后每对差恒为 100
        x = tuple(float(i) for i in range(10))
        y = tuple(v + 100.0 for v in x)
        sample = PairedSample(x, y)
        for seed in range(20):
            rx, ry = resample_paired(sample, random.Random(seed))
            self.assertEqual(len(rx), 10)
            for a, b in zip(rx, ry):
                self.assertEqual(b - a, 100.0)


class TestResampleStratified(unittest.TestCase):
    def test_stratum_sizes_fixed_no_leak(self):
        sample = StratifiedSample(((1.0, 2.0, 3.0), (100.0, 200.0)))
        out = resample_stratified(sample, random.Random(1))
        self.assertEqual(len(out[0]), 3)
        self.assertEqual(len(out[1]), 2)
        self.assertTrue(set(out[0]) <= {1.0, 2.0, 3.0})
        self.assertTrue(set(out[1]) <= {100.0, 200.0})


class TestResampleMBB(unittest.TestCase):
    def setUp(self):
        self.series = TimeSeriesSample(tuple(float(i) for i in range(10)))

    def test_output_length_preserved(self):
        out = resample_mbb(self.series, 3, random.Random(0), "circular")
        self.assertEqual(len(out), 10)
        out = resample_mbb(self.series, 4, random.Random(0), "truncated")
        self.assertEqual(len(out), 10)

    def test_truncated_blocks_contiguous(self):
        # 值互不相同：truncated 规则下每个块必须是原序列的连续切片
        vals = self.series.values
        out = resample_mbb(self.series, 3, random.Random(5), "truncated")
        for i in range(0, 9, 3):
            block = out[i:i + 3]
            start = int(block[0])
            self.assertEqual(block, vals[start:start + 3])

    def test_circular_may_wrap(self):
        # circular 规则允许回绕；block_length = n 时 truncated 只能是原序列
        out_t = resample_mbb(self.series, 10, random.Random(0), "truncated")
        self.assertEqual(out_t, self.series.values)
        seen_different = False
        for seed in range(50):
            out_c = resample_mbb(self.series, 10, random.Random(seed), "circular")
            if out_c != self.series.values:
                seen_different = True
                # 回绕块仍由合法起点生成
                start = int(out_c[0])
                expect = tuple(self.series.values[(start + j) % 10] for j in range(10))
                self.assertEqual(out_c, expect)
        self.assertTrue(seen_different)

    def test_block_length_one_is_iid(self):
        out = resample_mbb(self.series, 1, random.Random(0), "circular")
        self.assertEqual(len(out), 10)
        self.assertTrue(set(out) <= set(self.series.values))

    def test_invalid_params(self):
        with self.assertRaises(ValueError):
            resample_mbb(self.series, 0, random.Random(0))
        with self.assertRaises(ValueError):
            resample_mbb(self.series, 11, random.Random(0))
        with self.assertRaises(TypeError):
            resample_mbb(self.series, 2.5, random.Random(0))
        with self.assertRaises(ValueError):
            resample_mbb(self.series, 3, random.Random(0), "wrap-around?")


if __name__ == "__main__":
    unittest.main()
