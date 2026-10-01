import bisect
import unittest

from stftpipe import NUMERICAL_TOLERANCE, STFTPipeline, SpectralEditError, fixtures


def max_err(a, b):
    return max((abs(x - y) for x, y in zip(a, b)), default=0.0)


class BatchTransaction(unittest.TestCase):
    def test_failed_batch_preserves_published_version(self):
        sig = fixtures.mixed(3000)
        pipe = STFTPipeline(256, 64)
        pipe.add_channel(sig)
        pipe.analyze()
        v0 = pipe.published_version
        out0 = pipe.output()
        spec0 = [list(sp) for sp in pipe.channels[0].spectra]
        ops = [
            ("edit", 0, 2, {5: 0.5 + 0.5j}),
            ("drop", 4),
            ("edit", 0, 3, {4: 1 + 1j, 252: 9 + 9j}),  # conjugate violation
        ]
        with self.assertRaises(SpectralEditError):
            pipe.apply_batch(ops)
        self.assertEqual(pipe.published_version, v0)
        self.assertEqual(pipe.output(), out0)
        self.assertFalse(pipe.dropped)
        for a, b in zip(pipe.channels[0].spectra, spec0):
            self.assertEqual(a, b)

    def test_successful_batch_bumps_version_once(self):
        sig = fixtures.mixed(3000)
        pipe = STFTPipeline(256, 64)
        pipe.add_channel(sig)
        pipe.analyze()
        v0 = pipe.published_version
        v1 = pipe.apply_batch([
            ("drop", 4),
            ("restore", 4),
            ("edit", 0, 2, {5: 0.1 + 0.2j}),
        ])
        self.assertEqual(v1, v0 + 1)


class CheckpointResume(unittest.TestCase):
    def test_checkpoint_preserves_open_tail_and_resume_matches(self):
        sig = fixtures.mixed(4000)
        ref = STFTPipeline(256, 64)
        ref.add_channel(sig)
        ref.analyze()

        part = STFTPipeline(256, 64)
        part.add_channel(sig[:2500])
        part.analyze()
        ckpt = part.checkpoint()
        tail_idx = bisect.bisect_right(part.starts, part.out_len - part.size)
        self.assertEqual(ckpt["tail_start"], part.starts[tail_idx])
        self.assertEqual(
            len(ckpt["open_tail"]["output"][0]),
            part.out_len - ckpt["tail_start"],
        )
        self.assertGreater(len(ckpt["open_tail"]["output"][0]), 0)

        resumed = STFTPipeline.from_checkpoint(ckpt)
        resumed.extend({0: sig[2500:]}, 64)
        self.assertEqual(resumed.starts, ref.starts)
        self.assertLess(max_err(resumed.output(), ref.output()), NUMERICAL_TOLERANCE)
        self.assertEqual(resumed.mask(), ref.mask())


class MultiChannel(unittest.TestCase):
    def test_channels_time_aligned(self):
        delay = 100
        tone = fixtures.tone(440.0, 3000)
        shifted = [0.0] * delay + tone[:3000 - delay]
        pipe = STFTPipeline(256, 64)
        pipe.add_channel(tone)
        pipe.add_channel(shifted)
        pipe.analyze()
        out0, out1 = pipe.output(0), pipe.output(1)
        self.assertEqual(len(out0), len(out1))
        err = max(abs(out1[n] - out0[n - delay]) for n in range(delay + 512, 2500))
        self.assertLess(err, NUMERICAL_TOLERANCE)

    def test_sample_rate_mismatch_rejected(self):
        pipe = STFTPipeline(256, 64, sample_rate=16000)
        pipe.add_channel(fixtures.tone(440.0, 1000), 16000)
        with self.assertRaises(ValueError):
            pipe.add_channel(fixtures.tone(440.0, 1000), 8000)


if __name__ == "__main__":
    unittest.main()
