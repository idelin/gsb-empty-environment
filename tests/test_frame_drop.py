import unittest

from stftpipe import NUMERICAL_TOLERANCE, STFTPipeline, fixtures


class FrameDrop(unittest.TestCase):
    def test_drop_marks_exact_unrecoverable_sample(self):
        # Periodic hann with hop = N/2: dropping one frame zeroes the weight
        # of exactly one sample (the frame midpoint), which must be masked
        # rather than zero-filled and called lossless.
        sig = fixtures.mixed(3000)
        pipe = STFTPipeline(256, 128)
        pipe.add_channel(sig)
        pipe.analyze()
        pipe.drop_frame(3)
        s3 = pipe.starts[3]
        self.assertEqual(pipe.unrecoverable_regions(), [(s3 + 128, s3 + 129)])
        self.assertFalse(pipe.is_lossless())
        mask = pipe.mask()
        out = pipe.output()
        rest = [i for i, m in enumerate(mask) if not m]
        err = max(abs(out[i] - sig[i]) for i in rest)
        self.assertLess(err, NUMERICAL_TOLERANCE)

    def test_drop_no_overlap_marks_whole_frame(self):
        sig = fixtures.mixed(3000)
        pipe = STFTPipeline(256, 256, window="rect")
        pipe.add_channel(sig)
        pipe.analyze()
        pipe.drop_frame(2)
        s2 = pipe.starts[2]
        self.assertEqual(pipe.unrecoverable_regions(), [(s2, s2 + 256)])
        self.assertFalse(pipe.is_lossless())

    def test_restore_frame_recovers(self):
        sig = fixtures.mixed(3000)
        pipe = STFTPipeline(256, 128)
        pipe.add_channel(sig)
        pipe.analyze()
        pipe.drop_frame(3)
        self.assertFalse(pipe.is_lossless())
        pipe.restore_frame(3)
        self.assertTrue(pipe.is_lossless())
        err = max(abs(a - b) for a, b in zip(pipe.output(), sig))
        self.assertLess(err, NUMERICAL_TOLERANCE)

    def test_rebuild_only_touches_affected_interval(self):
        sig = fixtures.mixed(3000)
        pipe = STFTPipeline(256, 64)
        pipe.add_channel(sig)
        pipe.analyze()
        before = pipe.output()
        pipe.drop_frame(4)
        after = pipe.output()
        lo = max(0, pipe.starts[4])
        hi = min(pipe.starts[4] + 256, pipe.out_len)
        self.assertEqual(before[:lo], after[:lo])
        self.assertEqual(before[hi:], after[hi:])


if __name__ == "__main__":
    unittest.main()
