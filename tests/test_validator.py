import unittest
from pathlib import Path

from cdcstream import (
    ArrivalOrderProcessor,
    ChangeEvent,
    IncrementalProcessor,
    StreamGenerator,
    load_events,
    run_and_validate,
)

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "changes.json"


class ExplodingProcessor:
    """Raises when it sees a delete - used to trigger processing_error."""

    def run(self, items):
        for item in items:
            if isinstance(item, ChangeEvent) and item.after is None:
                raise RuntimeError("deletes not supported")

    def state(self):
        return {}


class ValidatorTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.events = load_events(FIXTURE)

    def test_ok_for_correct_processor(self):
        stream = StreamGenerator(5).generate(self.events)
        result = run_and_validate(stream, IncrementalProcessor)
        self.assertEqual(result.status, "ok")
        self.assertTrue(result.ok)

    def test_invalid_stream_is_separate_and_processor_never_runs(self):
        bad = StreamGenerator(1).generate_invalid(self.events, "partial_tx")
        calls = []

        def factory():
            calls.append(1)
            return IncrementalProcessor()

        result = run_and_validate(bad, factory)
        self.assertEqual(result.status, "invalid_stream")
        self.assertIn("partial transaction", result.detail)
        self.assertEqual(calls, [])  # processor never touched invalid input

    def test_processing_error_is_separate_from_invalid_stream(self):
        stream = StreamGenerator(5).generate(self.events)
        result = run_and_validate(stream, ExplodingProcessor)
        self.assertEqual(result.status, "processing_error")
        self.assertIn("deletes not supported", result.detail)

    def test_mismatch_reports_state_diff(self):
        # Hand-crafted out-of-order pair on the same key.
        ev1 = ChangeEvent(1, 1, "t", "k", None, {"v": 1}, commit_seq=1)
        ev2 = ChangeEvent(2, 2, "t", "k", {"v": 1}, {"v": 2}, commit_seq=2)
        result = run_and_validate([ev2, ev1], ArrivalOrderProcessor)
        self.assertEqual(result.status, "mismatch")
        self.assertEqual(result.differing, {("t", "k"): ({"v": 1}, {"v": 2})})


if __name__ == "__main__":
    unittest.main()
