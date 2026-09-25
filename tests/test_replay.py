import os
import shutil
import tempfile
import unittest

from samples import replay as replay_mod


class TestReplay(unittest.TestCase):
    def test_replay_matches_golden_log(self):
        workdir = tempfile.mkdtemp()
        try:
            lines = replay_mod.replay(workdir)
        finally:
            shutil.rmtree(workdir, ignore_errors=True)
        golden = os.path.join(os.path.dirname(__file__), os.pardir,
                              "samples", "expected_replay.log")
        with open(golden) as fh:
            expected = fh.read().splitlines()
        self.assertEqual(lines, expected)

    def test_replay_is_deterministic(self):
        first = replay_mod.replay(tempfile.mkdtemp())
        second = replay_mod.replay(tempfile.mkdtemp())
        self.assertEqual(first, second)


if __name__ == "__main__":
    unittest.main()
