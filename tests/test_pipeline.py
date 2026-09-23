import sqlite3
import tempfile
import unittest
from pathlib import Path

from cdc_pipeline.dispatcher import Dispatcher
from cdc_pipeline.models import ChangeEvent
from cdc_pipeline.pipeline import Pipeline, load_fixture
from cdc_pipeline.sink import IdempotentSink
from cdc_pipeline.store import StateStore

FIXTURE = str(Path(__file__).resolve().parent.parent / "fixtures" / "changes.json")


def make_event(tx="tx-1", table="t", pk="k", after=None, seq=1):
    return ChangeEvent(tx, table, pk, None, after or {"v": seq}, seq)


class PipelineTestCase(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        root = Path(self._tmp.name)
        self.store = StateStore(str(root / "state.db"))
        self.sink = IdempotentSink(str(root / "sink.db"), str(root / "audit.jsonl"))
        self.pipe = Pipeline(self.store)

    def tearDown(self):
        self.store.close()
        self.sink.close()
        self._tmp.cleanup()

    def test_fixture_loads_sorted_with_all_fields(self):
        events = load_fixture(FIXTURE)
        self.assertEqual(6, len(events))
        self.assertEqual([1, 2, 3, 4, 5, 6], [e.commit_seq for e in events])
        first = events[0]
        self.assertEqual("tx-1001", first.tx_id)
        self.assertEqual("accounts", first.table)
        self.assertEqual("a-1", first.pk)
        self.assertIsNone(first.before)
        self.assertEqual({"balance": 500}, first.after)

    def test_ingest_commits_offset_and_outputs_atomically(self):
        events = load_fixture(FIXTURE)
        offset = self.pipe.ingest(events[:3])
        self.assertEqual(3, offset)
        self.assertEqual(3, self.pipe.offset)
        self.assertEqual({"pending": 3}, self.store.outbox_counts())

    def test_offset_never_advances_past_committed_outputs(self):
        # Simulate a crash mid-commit by breaking the connection inside
        # commit_batch: the whole transaction must roll back.
        events = load_fixture(FIXTURE)

        class CrashConn:
            def __init__(self, real):
                self._real = real

            def executemany(self, *args, **kwargs):
                raise sqlite3.OperationalError("simulated mid-commit crash")

            def __getattr__(self, name):
                return getattr(self._real, name)

        real_conn = self.store._conn
        self.store._conn = CrashConn(real_conn)
        with self.assertRaises(sqlite3.OperationalError):
            self.pipe.ingest(events)
        self.store._conn = real_conn
        # Offset unchanged, no partial outbox rows: batch fully retryable.
        self.assertEqual(0, self.pipe.offset)
        self.assertEqual({}, self.store.outbox_counts())
        self.assertEqual(6, self.pipe.ingest(events))
        self.assertEqual({"pending": 6}, self.store.outbox_counts())

    def test_failed_batch_rolls_back_and_stays_retryable(self):
        bad = [make_event(tx="a", seq=1),
               ChangeEvent("b", "t", "k2", None, None, 2)]  # invalid event
        with self.assertRaises(ValueError):
            self.pipe.ingest(bad)
        self.assertEqual(0, self.pipe.offset)
        self.assertEqual({}, self.store.outbox_counts())
        fixed = [bad[0], make_event(tx="b", pk="k2", seq=2)]
        self.assertEqual(2, self.pipe.ingest(fixed))
        self.assertEqual({"pending": 2}, self.store.outbox_counts())

    def test_reingest_of_confirmed_input_is_skipped(self):
        events = load_fixture(FIXTURE)
        self.pipe.ingest(events[:4])
        # Re-feeding already-confirmed events must not create new outputs.
        self.assertEqual(4, self.pipe.ingest(events[:4]))
        self.assertEqual({"pending": 4}, self.store.outbox_counts())

    def test_duplicate_event_in_batch_deduped_by_idempotency_id(self):
        event = make_event(seq=1)
        self.pipe.ingest([event, event, event])
        self.assertEqual({"pending": 1}, self.store.outbox_counts())

    def test_delivery_acks_and_sink_applies_once(self):
        events = load_fixture(FIXTURE)
        self.pipe.ingest(events)
        acked = Dispatcher(self.store, self.sink).deliver_pending()
        self.assertEqual(6, acked)
        self.assertEqual({"acked": 6}, self.store.outbox_counts())
        self.assertEqual(6, self.sink.count())

    def test_crash_after_send_before_ack_recovers_without_duplicates(self):
        events = load_fixture(FIXTURE)
        self.pipe.ingest(events)

        sent_before_crash = []

        def crash_hook(idempotency_id):
            sent_before_crash.append(idempotency_id)
            if len(sent_before_crash) == 2:
                raise RuntimeError("simulated crash")

        crashing = Dispatcher(self.store, self.sink, after_send_hook=crash_hook)
        with self.assertRaises(RuntimeError):
            crashing.deliver_pending()
        # Two effects reached the sink; only the first was acked.
        self.assertEqual(2, self.sink.count())
        self.assertEqual({"pending": 5, "acked": 1}, self.store.outbox_counts())

        # "Restart": fresh dispatcher over the same persisted state.
        recovered = Dispatcher(self.store, self.sink).deliver_pending()
        self.assertEqual(5, recovered)
        # Exactly-once business effects despite redelivery of #2.
        self.assertEqual(6, self.sink.count())
        self.assertEqual({"acked": 6}, self.store.outbox_counts())

    def test_unacked_output_resent_but_deduped_downstream(self):
        events = load_fixture(FIXTURE)
        self.pipe.ingest(events[:1])
        record_id = events[0].idempotency_id
        # Deliver once without ack (crash before mark_acked).
        crashing = Dispatcher(
            self.store, self.sink,
            after_send_hook=lambda _id: (_ for _ in ()).throw(RuntimeError("x")))
        with self.assertRaises(RuntimeError):
            crashing.deliver_pending()
        # Redeliver: sink dedupes the same idempotency_id.
        Dispatcher(self.store, self.sink).deliver_pending()
        self.assertEqual({record_id}, self.sink.applied_ids())
        self.assertEqual(1, self.sink.count())

    def test_state_survives_reopen(self):
        events = load_fixture(FIXTURE)
        self.pipe.ingest(events[:3])
        Dispatcher(self.store, self.sink).deliver_pending()
        self.store.close()
        self.sink.close()

        root = Path(self._tmp.name)
        self.store = StateStore(str(root / "state.db"))
        self.sink = IdempotentSink(str(root / "sink.db"), str(root / "audit.jsonl"))
        self.pipe = Pipeline(self.store)
        self.assertEqual(3, self.pipe.offset)
        self.assertEqual({"acked": 3}, self.store.outbox_counts())
        self.assertEqual(3, self.sink.count())
        # Resume with remaining events; no loss, no duplicates.
        self.pipe.ingest(events)
        Dispatcher(self.store, self.sink).deliver_pending()
        self.assertEqual(6, self.pipe.offset)
        self.assertEqual(6, self.sink.count())


if __name__ == "__main__":
    unittest.main()
