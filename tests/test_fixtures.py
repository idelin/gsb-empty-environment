import unittest

from filterverify.fixtures import SAMPLE_RATE, fixture


class FixtureTest(unittest.TestCase):
    def test_fixtures_deterministic_and_local(self):
        for name in ("impulse", "chirp", "multitone_pass", "probe"):
            a, fs_a = fixture(name, 512)
            b, fs_b = fixture(name, 512)
            self.assertEqual(fs_a, SAMPLE_RATE)
            self.assertEqual(fs_a, fs_b)
            self.assertEqual(a, b)  # replayable
            self.assertEqual(len(a), 512)

    def test_unknown_fixture(self):
        with self.assertRaises(KeyError):
            fixture("microphone")  # no audio devices here


if __name__ == "__main__":
    unittest.main()
