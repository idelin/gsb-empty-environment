"""Pipeline orchestration: ingest local fixtures and deliver outbox outputs.

Ingest rules
------------
* Fixture batches are committed in strict ``commit_seq`` order; a gap aborts.
* The offset (high-water mark) moves only in the same durable transaction as
  the business projection and the outbox rows.
* A crash may be injected right after a batch commit; on restart the batch is
  replayed from the fixture but rejected as stale, so nothing is applied twice.

Delivery rules
--------------
* Pending outputs are sent in commit order, in batches of ``batch_size``.
* A delivery failure stops the batch; earlier successes are acknowledged
  together, the failed row stays pending and the whole batch stays retryable.
* A lost acknowledgement is a retryable send; the sink dedupes by idem id, so
  no business effect is duplicated.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from .errors import (
    CrashInjected,
    DeliveryFailedError,
    GapDetectedError,
    SinkError,
)
from .model import Change, change_uid
from .sink import LocalSink
from .store import OutputPlanner, Store

Clock = Callable[[], datetime]


def utc_now() -> datetime:
    return datetime.now(timezone.utc)


@dataclass(frozen=True, slots=True)
class IngestReport:
    committed: list[int]
    outputs_enqueued: int
    high_water_seq: int


@dataclass(frozen=True, slots=True)
class SendReport:
    sent: list[int] = field(default_factory=list)
    duplicates: list[str] = field(default_factory=list)
    failures: list[str] = field(default_factory=list)
    acked_ids: list[int] = field(default_factory=list)
    attempts_before_failure: int = 0

    @property
    def ok(self) -> bool:
        return not self.failures


class Processor:
    def __init__(
        self,
        store: Store,
        sink: LocalSink,
        *,
        batch_size: int = 100,
        clock: Clock = utc_now,
    ):
        self._store = store
        self._sink = sink
        self._batch_size = batch_size
        self._clock = clock

    def recovery_info(self) -> dict[str, Any]:
        return self._store.recovery_info()

    def ingest(
        self,
        changes: list[Change],
        *,
        crash_after_seq: int | None = None,
        planner: OutputPlanner | None = None,
    ) -> IngestReport:
        """Commit every not-yet-applied fixture batch in commit order.

        Replaying already-committed batches after a restart is a no-op: they
        sit at or below the high-water mark and are skipped, never reapplied.
        """
        ordered = sorted(changes, key=lambda c: (c.commit_seq, change_uid(c)))
        by_seq: dict[int, list[Change]] = {}
        for change in ordered:
            by_seq.setdefault(change.commit_seq, []).append(change)

        high_water = self._store.high_water_seq
        committed: list[int] = []
        enqueued = 0
        for seq in sorted(by_seq):
            if seq <= high_water:
                continue
            if seq != high_water + 1:
                raise GapDetectedError(
                    f"missing commit seq={high_water + 1} before seq={seq}"
                )
            batch = by_seq[seq]
            kwargs: dict[str, Any] = {
                "committed_at": self._clock().isoformat(),
            }
            if planner is not None:
                kwargs["planner"] = planner
            planned = self._store.commit_batch(batch, **kwargs)
            high_water = seq
            committed.append(seq)
            enqueued += len(planned)
            if crash_after_seq == seq:
                raise CrashInjected(
                    f"simulated crash immediately after committing seq={seq}"
                )

        return IngestReport(
            committed=committed,
            outputs_enqueued=enqueued,
            high_water_seq=high_water,
        )

    def send_pending(self) -> SendReport:
        """Send one ordered batch of pending outputs and acknowledge successes.

        The batch is retryable: on failure every un-acked row (including the
        failed one) is still ``pending`` and a later call tries them again.
        """
        pending = self._store.fetch_pending(limit=self._batch_size)
        if not pending:
            return SendReport()

        sent: list[int] = []
        duplicates: list[str] = []
        failures: list[str] = []
        acked_ids: list[int] = []
        attempts = 0

        for row in pending:
            attempts += 1
            timestamp = self._clock().isoformat()
            try:
                result = self._sink.deliver(row, timestamp)
            except DeliveryFailedError as exc:
                self._store.record_attempt(row.id, str(exc), timestamp)
                failures.append(row.idem_id)
                break
            except SinkError as exc:
                self._store.record_attempt(row.id, str(exc), timestamp)
                failures.append(row.idem_id)
                break

            sent.append(row.commit_seq)
            if result.duplicate:
                duplicates.append(row.idem_id)
            acked_ids.append(row.id)

        if acked_ids:
            self._store.ack_many(acked_ids)

        return SendReport(
            sent=sent,
            duplicates=duplicates,
            failures=failures,
            acked_ids=acked_ids,
            attempts_before_failure=attempts if failures else 0,
        )

    def run_to_completion(self, changes: list[Change]) -> tuple[list[IngestReport], list[SendReport]]:
        """Ingest the fixture and keep sending until the outbox is drained."""
        ingest_reports = [self.ingest(changes)]
        send_reports: list[SendReport] = []
        while self._store.pending_count() > 0:
            report = self.send_pending()
            send_reports.append(report)
            if not report.ok:
                break
        return ingest_reports, send_reports
