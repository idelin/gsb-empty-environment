import unittest

from stftpipe import NUMERICAL_TOLERANCE, STFTPipeline, fixtures


def max_err(a, b):
    return max((abs(x - y) for x, y in zip(a, b)), default=0.0)


class FixedHopRoundTrip(unittest.TestCase):
    def test_roundtrip_fixed_legal(self):
        sig = fixtures.mixed(3000)
        pipe = STFTPipeline(256, 64)
        pipe.add_channel(sig, fixtures.SAMPLE_RATE_A)
        pipe.analyze()
        self.assertTrue(pipe.is_lossless())
        self.assertLess(max_err(pipe.output(), sig), NUMERICAL_TOLERANCE)

    def test_latency_declaration(self):
        pipe = STFTPipeline(256, 64)
        self.assertEqual(pipe.algorithmic_latency_samples, 256)


class VariableHop(unittest.TestCase):
    def test_roundtrip_variable_hop(self):
        sig = fixtures.mixed(4000)
        pipe = STFTPipeline(256, [48, 96, 32, 80])
        pipe.add_channel(sig)
        pipe.analyze()
        self.assertTrue(pipe.is_lossless())
        self.assertLess(max_err(pipe.output(), sig), NUMERICAL_TOLERANCE)

    def test_weights_not_constant_under_variable_hop(self):
        # Per-sample weights vary under variable hop, so normalization must
        # use the actual weights; a fixed COLA coefficient would be wrong.
        sig = fixtures.mixed(4000)
        pipe = STFTPipeline(256, [48, 96, 32, 80])
        pipe.add_channel(sig)
        pipe.analyze()
        w = pipe.weights()[256:-256]
        self.assertGreater(max(w) - min(w), 0.1)
        self.assertTrue(all(v > 0.0 for v in w))


class ShortTailFrame(unittest.TestCase):
    def test_tail_weights_match_direct_computation(self):
        n = 3000 + 137  # last frame is a short tail frame (zero-padded)
        sig = fixtures.mixed(n)
        size, hop = 256, 64
        pipe = STFTPipeline(size, hop)
        pipe.add_channel(sig)
        pipe.analyze()
        self.assertLess(pipe.starts[-1], n)
        win = pipe.window
        w = pipe.weights()
        for sample in range(n - 300, n):
            expect = 0.0
            for s in pipe.starts:
                j = sample - s
                if 0 <= j < size:
                    expect += win[j] ** 2
            self.assertAlmostEqual(w[sample], expect, places=12)
        self.assertTrue(pipe.is_lossless())
        self.assertLess(max_err(pipe.output(), sig), NUMERICAL_TOLERANCE)


if __name__ == "__main__":
    unittest.main()
