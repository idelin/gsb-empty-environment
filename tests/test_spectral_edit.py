import unittest

from stftpipe import NUMERICAL_TOLERANCE, STFTPipeline, SpectralEditError, fixtures
from stftpipe.fft import ifft


class SpectralEdit(unittest.TestCase):
    def setUp(self):
        self.sig = fixtures.mixed(3000)
        self.pipe = STFTPipeline(256, 64)
        self.pipe.add_channel(self.sig)
        self.pipe.analyze()

    def test_edit_auto_fills_conjugate_mirror(self):
        self.pipe.edit_spectrum(0, 2, {5: 0.5 + 0.25j})
        spec = self.pipe.channels[0].spectra[2]
        self.assertAlmostEqual(spec[5], 0.5 + 0.25j)
        self.assertAlmostEqual(spec[256 - 5], 0.5 - 0.25j)
        imag = max(abs(z.imag) for z in ifft(spec))
        self.assertLess(imag, 1e-9)

    def test_consistent_mirror_pair_accepted(self):
        self.pipe.edit_spectrum(0, 2, {7: 1 + 2j, 256 - 7: 1 - 2j})
        spec = self.pipe.channels[0].spectra[2]
        self.assertAlmostEqual(spec[7], 1 + 2j)
        self.assertAlmostEqual(spec[256 - 7], 1 - 2j)

    def test_edit_localized_rebuild(self):
        before = self.pipe.output()
        self.pipe.edit_spectrum(0, 2, {5: 0.5 + 0.25j})
        after = self.pipe.output()
        lo = max(0, self.pipe.starts[2])
        hi = min(self.pipe.starts[2] + 256, self.pipe.out_len)
        self.assertEqual(before[:lo], after[:lo])
        self.assertEqual(before[hi:], after[hi:])
        self.assertNotEqual(before[lo:hi], after[lo:hi])

    def test_inconsistent_mirror_rejected(self):
        v0 = self.pipe.published_version
        before = self.pipe.output()
        with self.assertRaises(SpectralEditError):
            self.pipe.edit_spectrum(0, 2, {5: 1 + 1j, 251: 2 + 0j})
        self.assertEqual(self.pipe.published_version, v0)
        self.assertEqual(self.pipe.output(), before)

    def test_dc_and_nyquist_must_stay_real(self):
        with self.assertRaises(SpectralEditError):
            self.pipe.edit_spectrum(0, 1, {0: 0.5j})
        with self.assertRaises(SpectralEditError):
            self.pipe.edit_spectrum(0, 1, {128: 0.5j})

    def test_bin_out_of_range(self):
        with self.assertRaises(SpectralEditError):
            self.pipe.edit_spectrum(0, 1, {300: 1.0})


if __name__ == "__main__":
    unittest.main()
