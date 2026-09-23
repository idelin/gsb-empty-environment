"""unittest suite for the transactional outbox pipeline (stdlib only)."""

from __future__ import annotations

import json
import tempfile
import unittest
from dataclasses import asdict
from collections.abc import Iterator
from datetime import datetime, timezone
from pathlib import Path

from outbox.errors import (
    CrashInjected,
    DeliveryFailedError,
    DuplicateChangeError,
    GapDetectedError,
    PlannerError,
    StaleBatchError,
)
from outbox.model import Change, Output, change_uid, load_changes, output_id
from outbox.processor import Processor
from outbox.sink import LocalSink
from outbox.store import Store

FIXTURE = (
    Path(__file__).resolve().parents[1] / "sample" / "fixtures" / "accounts.json"
)


def change(
    seq: int,
    *,
    txn_id: str | None = None,
    table: str = "accounts",
    key: str = "k1",
    before=None,
    after=None,
) -> Change:
    if before is None and after is None:
        after = {"v": seq}
    return Change(
        txn_id=txn_id or f"T-{seq}",
        table=table,
        key=key,
        before=before,
        after=after,
        commit_seq=seq,
    )


def fixed_clock() -> datetime:
    return datetime(2026, 9, 23, 12, 0, 0, tzinfo=timezone.utc)


class TempCase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.tmp = Path(self._tmp.name)
        self.db_path = self.tmp / "p.sqlite3"
        self.sink_path = self.tmp / "sink.json"

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def open_processor(self, *, batch_size: int = 100) -> tuple[Store, LocalSink, Processor]:
        store = Store.open(str(self.db_path))
        sink = LocalSink(self.sink_path)
        return store, sink, Processor(store, sink, batch_size=batch_size)


class TestModel(unittest.TestCase):
    def test_fixture_loads_sorted(self) -> None:
        changes = load_changes(FIXTURE)
        self.assertEqual([c.commit_seq for c in changes], [1, 2, 3, 4])

    def test_idempotency_id_is_deterministic(self) -> None:
        c = change(1)
        self.assertEqual(output_id(c), output_id(c))
        self.assertNotEqual(output_id(change(1, key="a")), output_id(change(1, key="b")))
        self.assertTrue(output_id(c).startswith("out-0000000001-"))

    def test_invalid_change_rejected(self) -> None:
        with self.assertRaises(Exception):
            Change.from_dict(
                {"txn_id": "t", "table": "t", "key": "k",
                 "before": None, "after": None, "commit_seq": 1}
            )
        with self.assertRaises(Exception):
            Change.from_dict(
                {"txn_id": "t", "table": "t", "key": "k",
                 "before": None, "after": {}, "commit_seq": 0}
            )

    def test_duplicate_in_fixture_rejected(self) -> None:
        path = Path(tempfile.mkstemp(suffix=".json")[1])
        path.write_text(
            json.dumps({"changes": [
                asdict(change(1)), asdict(change(1)),
            ]}),
            encoding="utf-8",
        )
        with self.assertRaises(Exception):
            load_changes(path)


class TestStore(TempCase):
    def test_offset_and_outputs_commit_together(self) -> None:
        store, _, _ = self.open_processor()
        outputs = store.commit_batch([change(1)], fixed_clock().isoformat())
        self.assertEqual(store.high_water_seq, 1)
        self.assertEqual(store.committed_sequences(), [1])
        self.assertEqual(store.pending_count(), 1)
        self.assertEqual(outputs[0].idem_id, output_id(change(1)))

    def test_planner_failure_rolls_back_everything(self) -> None:
        store, _, _ = self.open_processor()

        def bad_planner(_change: Change) -> Output:
            raise RuntimeError("planner exploded")

        with self.assertRaises(PlannerError):
            store.commit_batch(
                [change(1)], fixed_clock().isoformat(), planner=bad_planner
            )
        self.assertEqual(store.high_water_seq, 0)
        self.assertEqual(store.pending_count(), 0)
        self.assertEqual(store.committed_sequences(), [])
        self.assertEqual(store.projection_snapshot(), {})

    def test_offset_never_ahead_of_output_promise(self) -> None:
        store, _, _ = self.open_processor()

        def planner_with_db_check(c: Change) -> Output:
            row = store.high_water_seq
            self.assertEqual(row, 0)
            return Output(
                uid=change_uid(c), idem_id=output_id(c),
                event_type="x", payload={},
            )

        store.commit_batch([change(1)], fixed_clock().isoformat(),
                           planner=planner_with_db_check)

    def test_replay_stale_batch_is_rejected_not_reapplied(self) -> None:
        store, _, _ = self.open_processor()
        store.commit_batch([change(1)], fixed_clock().isoformat())
        with self.assertRaises(StaleBatchError):
            store.commit_batch([change(1)], fixed_clock().isoformat())

    def test_gap_aborts_the_batch(self) -> None:
        store, _, _ = self.open_processor()
        with self.assertRaises(GapDetectedError):
            store.commit_batch([change(2)], fixed_clock().isoformat())
        self.assertEqual(store.high_water_seq, 0)

    def test_duplicate_logical_change_within_batch(self) -> None:
        store, _, _ = self.open_processor()
        same = change(1)
        with self.assertRaises(DuplicateChangeError):
            store.commit_batch([same, change(1)],
                               fixed_clock().isoformat())
        self.assertEqual(store.high_water_seq, 0)

    def test_delete_tombstone_removes_projection(self) -> None:
        store, _, _ = self.open_processor()
        store.commit_batch([change(1, after={"v": 1})],
                           fixed_clock().isoformat())
        store.commit_batch(
            [change(2, before={"v": 1}, after=None)],
            fixed_clock().isoformat(),
        )
        self.assertEqual(store.projection_snapshot(), {})
        self.assertEqual(store.high_water_seq, 2)
        self.assertEqual(store.pending_count(), 2)

    def test_multichange_transaction_is_atomic(self) -> None:
        store, _, _ = self.open_processor()

        def bad_planner(c: Change) -> Output:
            if c.key == "k2":
                raise RuntimeError("boom")
            return Output(uid=change_uid(c), idem_id=output_id(c),
                          event_type="x", payload={})

        with self.assertRaises(PlannerError):
            store.commit_batch(
                [change(1, key="k1"), change(1, txn_id="T-1b", key="k2")],
                fixed_clock().isoformat(),
                planner=bad_planner,
            )
        self.assertEqual(store.high_water_seq, 0)
        self.assertEqual(store.projection_snapshot(), {})

    def test_planner_output_contract_validated(self) -> None:
        store, _, _ = self.open_processor()

        def wrong_uid(c: Change) -> Output:
            return Output(uid="other", idem_id=output_id(c),
                          event_type="x", payload={})

        with self.assertRaises(PlannerError):
            store.commit_batch([change(1)], fixed_clock().isoformat(),
                               planner=wrong_uid)

    def test_state_survives_store_reopen(self) -> None:
        store, _, _ = self.open_processor()
        store.commit_batch([change(1)], fixed_clock().isoformat())
        store.close()
        reopened = Store.open(str(self.db_path))
        self.assertEqual(reopened.high_water_seq, 1)
        self.assertEqual(reopened.pending_count(), 1)
        reopened.close()


class TestSink(TempCase):
    def test_durable_dedupe_across_sink_reopen(self) -> None:
        store, _, _ = self.open_processor()
        store.commit_batch([change(1)], fixed_clock().isoformat())
        pending = store.fetch_pending()

        sink = LocalSink(self.sink_path)
        first = sink.deliver(pending[0], fixed_clock().isoformat())
        self.assertFalse(first.duplicate)
        reopened = LocalSink(self.sink_path)
        second = reopened.deliver(pending[0], fixed_clock().isoformat())
        self.assertTrue(second.duplicate)
        self.assertEqual(len(reopened), 1)


class TestProcessor(TempCase):
    def test_ingest_skips_already_committed_batches_on_replay(self) -> None:
        store, sink, processor = self.open_processor()
        changes = load_changes(FIXTURE)
        report = processor.ingest(changes)
        self.assertEqual(report.committed, [1, 2, 3, 4])
        replay = processor.ingest(changes)
        self.assertEqual(replay.committed, [])
        self.assertEqual(replay.high_water_seq, 4)
        self.assertEqual(store.pending_count(), 4)

    def test_crash_after_commit_loses_no_input_and_never_duplicates(self) -> None:
        changes = [change(1), change(2), change(3)]

        store, sink, processor = self.open_processor()
        with self.assertRaises(CrashInjected):
            processor.ingest(changes, crash_after_seq=2)
        self.assertEqual(store.high_water_seq, 2)
        self.assertEqual(store.pending_count(), 2)
        store.close()

        store, sink, processor = self.open_processor()
        report = processor.ingest(changes)
        self.assertEqual(report.committed, [3])
        self.assertEqual(store.high_water_seq, 3)
        self.assertEqual(store.committed_sequences(), [1, 2, 3])

        sent1 = processor.send_pending()
        self.assertTrue(sent1.ok)
        self.assertEqual(sorted(sent1.sent), [1, 2, 3])
        self.assertEqual(sent1.duplicates, [])
        self.assertEqual(store.pending_count(), 0)

        sent2 = processor.send_pending()
        self.assertEqual(sent2.sent, [])

    def test_failed_send_leaves_batch_retryable(self) -> None:
        store, sink, processor = self.open_processor(batch_size=5)
        changes = [change(1), change(2), change(3)]
        processor.ingest(changes)

        sink.fail_once(output_id(change(2)))
        first = processor.send_pending()
        self.assertFalse(first.ok)
        self.assertEqual(first.failures, [output_id(change(2))])
        self.assertEqual(first.sent, [1])
        self.assertEqual(store.pending_count(), 2)
        pending = store.fetch_pending()
        self.assertEqual([p.commit_seq for p in pending], [2, 3])

        second = processor.send_pending()
        self.assertTrue(second.ok)
        self.assertEqual(second.sent, [2, 3])
        self.assertEqual(store.pending_count(), 0)

    def test_lost_ack_is_retried_and_deduped(self) -> None:
        store, sink, processor = self.open_processor()
        changes = [change(1)]
        processor.ingest(changes)
        idem = output_id(change(1))

        sink.lose_ack_once(idem)
        first = processor.send_pending()
        self.assertFalse(first.ok)
        self.assertEqual(first.failures, [idem])
        self.assertIn(idem, sink)
        self.assertEqual(store.pending_count(), 1)

        second = processor.send_pending()
        self.assertTrue(second.ok)
        self.assertEqual(second.duplicates, [idem])
        self.assertEqual(store.pending_count(), 0)
        self.assertEqual(sink.stats()["unique_delivered"], 1)
        self.assertEqual(sink.stats()["duplicate_deliveries"], 1)

    def test_delivery_failure_does_not_persist_sink_effect(self) -> None:
        store, sink, processor = self.open_processor()
        processor.ingest([change(1)])
        idem = output_id(change(1))

        sink.fail_once(idem)
        report = processor.send_pending()
        self.assertFalse(report.ok)
        self.assertNotIn(idem, sink)
        self.assertEqual(len(sink), 0)

    def test_partial_batch_then_crash_restart_completes(self) -> None:
        changes = [change(1), change(2), change(3)]
        store, sink, processor = self.open_processor(batch_size=5)
        processor.ingest(changes)
        sink.fail_once(output_id(change(2)))
        processor.send_pending()
        store.close()

        store, sink, processor = self.open_processor(batch_size=5)
        self.assertEqual(store.pending_count(), 2)
        report = processor.send_pending()
        self.assertTrue(report.ok)
        self.assertEqual(report.sent, [2, 3])
        self.assertEqual(sink.stats()["unique_delivered"], 3)
        self.assertEqual(store.pending_count(), 0)

    def test_end_to_end_fixture(self) -> None:
        store, sink, processor = self.open_processor()
        changes = load_changes(FIXTURE)
        ingest_reports, send_reports = processor.run_to_completion(changes)
        self.assertEqual(ingest_reports[0].committed, [1, 2, 3, 4])
        self.assertTrue(all(r.ok for r in send_reports))
        self.assertEqual(store.pending_count(), 0)
        self.assertEqual(len(sink), 4)
        projection = store.projection_snapshot()
        self.assertEqual(set(projection), {("accounts", "acct-2")})
        self.assertEqual(projection[("accounts", "acct-2")]["balance"], 30)

    def test_ingest_gap_in_fixture_stream_aborts(self) -> None:
        store, sink, processor = self.open_processor()
        with self.assertRaises(GapDetectedError):
            processor.ingest([change(1), change(3)])
        self.assertEqual(store.high_water_seq, 1)
        self.assertEqual(store.pending_count(), 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
