import unittest
from pathlib import Path

from cdcstream import (
    ArrivalOrderProcessor,
    IncrementalProcessor,
    StreamGenerator,
    load_events,
    run_and_validate,
    shrink,
    validate_stream,
)

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "changes.json"


def _failing_stream(events):
    """Deterministically find a generated stream that breaks the buggy processor."""
    for seed in range(100):
        stream = StreamGenerator(seed).generate(
            events, reorder_window=4, duplicate_probability=0.4, crashes=2
        )
        if run_and_validate(stream, ArrivalOrderProcessor).failed:
            return seed, stream
    raise AssertionError("no failing seed found in range")


class ShrinkerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.events = load_events(FIXTURE)

    def test_shrinks_to_smaller_still_failing_replayable_stream(self):
        seed, stream = _failing_stream(self.events)
        original_status = run_and_validate(stream, ArrivalOrderProcessor).status
        shrunk = shrink(stream, ArrivalOrderProcessor)

        self.assertLess(len(shrunk), len(stream))
        # Failure is preserved with the same failure class.
        result = run_and_validate(shrunk, ArrivalOrderProcessor)
        self.assertEqual(result.status, original_status)
        # Transaction structure stays replayable: well-formed, complete txs.
        validate_stream(shrunk)

    def test_shrunk_stream_is_1_minimal_per_transaction(self):
        _, stream = _failing_stream(self.events)
        shrunk = shrink(stream, ArrivalOrderProcessor)
        # Removing any single remaining transaction must stop the failure
        # (or make the stream invalid/empty), i.e. no tx is removable.
        tx_ids = sorted({it.tx_id for it in shrunk if hasattr(it, "tx_id")})
        for tx_id in tx_ids:
            candidate = [
                it for it in shrunk
                if not (hasattr(it, "tx_id") and it.tx_id == tx_id)
            ]
            result = run_and_validate(candidate, ArrivalOrderProcessor)
            self.assertFalse(
                result.failed,
                f"tx {tx_id} still removable; shrinker not at fixpoint",
            )

    def test_correct_processor_streams_do_not_shrink(self):
        stream = StreamGenerator(5).generate(self.events)
        self.assertTrue(run_and_validate(stream, IncrementalProcessor).ok)
        with self.assertRaises(ValueError):
            shrink(stream, IncrementalProcessor)


if __name__ == "__main__":
    unittest.main()
