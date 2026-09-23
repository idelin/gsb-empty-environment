import unittest
from pathlib import Path

from cdcstream import (
    ChangeEvent,
    CrashPoint,
    StreamGenerator,
    events_of,
    load_events,
    validate_stream,
)
from cdcstream.validator import InvalidStreamError

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "changes.json"


class GeneratorTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.events = load_events(FIXTURE)

    def test_deterministic_per_seed(self):
        s1 = StreamGenerator(42).generate(self.events)
        s2 = StreamGenerator(42).generate(self.events)
        self.assertEqual(s1, s2)
        s3 = StreamGenerator(43).generate(self.events)
        self.assertNotEqual(s1, s3)

    def test_contains_duplicates_reorder_and_crashes(self):
        stream = StreamGenerator(7).generate(
            self.events, reorder_window=3, duplicate_probability=0.5, crashes=2
        )
        delivered = [it for it in stream if isinstance(it, ChangeEvent)]
        ids = [ev.event_id for ev in delivered]
        # duplicates: some event delivered more than once
        self.assertGreater(len(ids), len(set(ids)))
        # crash points present
        self.assertTrue(any(isinstance(it, CrashPoint) for it in stream))
        # out-of-order: delivery order is not globally commit_seq ordered
        seqs = [ev.commit_seq for ev in delivered]
        self.assertTrue(any(a > b for a, b in zip(seqs, seqs[1:])))

    def test_logical_content_preserved(self):
        stream = StreamGenerator(11).generate(self.events)
        self.assertEqual(
            sorted(ev.event_id for ev in events_of(stream)),
            [ev.event_id for ev in self.events],
        )
        validate_stream(stream)  # generated streams are well-formed

    def test_invalid_variants_are_caught_as_invalid_streams(self):
        for kind in ("partial_tx", "broken_chain", "conflicting_dup"):
            with self.subTest(kind=kind):
                bad = StreamGenerator(1).generate_invalid(self.events, kind)
                with self.assertRaises(InvalidStreamError):
                    validate_stream(bad)


if __name__ == "__main__":
    unittest.main()
