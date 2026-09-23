import unittest
from pathlib import Path

from cdcstream import IncrementalProcessor, StreamGenerator, load_events, run_and_validate

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "changes.json"


class ProcessorTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.events = load_events(FIXTURE)

    def test_matches_oracle_across_seeds(self):
        for seed in range(25):
            with self.subTest(seed=seed):
                stream = StreamGenerator(seed).generate(
                    self.events,
                    reorder_window=4,
                    duplicate_probability=0.4,
                    crashes=2,
                )
                result = run_and_validate(stream, IncrementalProcessor)
                self.assertEqual(result.status, "ok", result.detail)

    def test_crash_only_loses_volatile_state(self):
        stream = StreamGenerator(3).generate(self.events, crashes=3)
        result = run_and_validate(stream, IncrementalProcessor)
        self.assertTrue(result.ok)


if __name__ == "__main__":
    unittest.main()
