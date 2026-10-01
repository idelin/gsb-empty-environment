import random
import unittest

from stftpipe import STFTPipeline, fixtures
from stftpipe.fft import fft, ifft
from stftpipe.reference import dft, idft


class DirectDFTReference(unittest.TestCase):
    def test_fft_matches_direct_dft_small(self):
        rng = random.Random(42)
        x = [rng.uniform(-1, 1) for _ in range(16)]
        for a, b in zip(fft(x), dft(x)):
            self.assertAlmostEqual(abs(a - b), 0.0, delta=1e-12)

    def test_fft_non_power_of_two_fallback(self):
        rng = random.Random(43)
        x = [rng.uniform(-1, 1) for _ in range(12)]
        for a, b in zip(fft(x), dft(x)):
            self.assertAlmostEqual(abs(a - b), 0.0, delta=1e-12)

    def test_ifft_inverts_dft(self):
        rng = random.Random(44)
        x = [rng.uniform(-1, 1) for _ in range(16)]
        spectrum = dft(x)
        for a, b in zip(ifft(spectrum), x):
            self.assertAlmostEqual(a.real, b, delta=1e-12)

    def test_idft_inverts_fft(self):
        rng = random.Random(45)
        x = [rng.uniform(-1, 1) for _ in range(16)]
        spectrum = fft(x)
        for a, b in zip(idft(spectrum), x):
            self.assertAlmostEqual(a.real, b, delta=1e-12)

    def test_pipeline_frame_matches_direct_dft(self):
        sig = fixtures.mixed(500)
        size, hop = 32, 16
        pipe = STFTPipeline(size, hop)
        pipe.add_channel(sig)
        pipe.analyze()
        frame_idx = 5
        s = pipe.starts[frame_idx]
        win = pipe.window
        seg = [sig[s + i] * win[i] for i in range(size)]
        expect = dft(seg)
        got = pipe.channels[0].spectra[frame_idx]
        for a, b in zip(got, expect):
            self.assertAlmostEqual(abs(a - b), 0.0, delta=1e-12)


if __name__ == "__main__":
    unittest.main()
