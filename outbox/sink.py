"""Durable local output sink with idempotency-id deduplication.

The sink stands in for the output side. Its journal is a JSON file written
atomically (temp file + ``os.replace`` + ``fsync``), so an acknowledged
delivery survives a process crash. Two failure modes can be injected for
demonstrating the delivery protocol:

* ``fail_once(idem_id)`` - the delivery itself is rejected (retry later);
* ``lose_ack_once(idem_id)`` - the payload is durably accepted but the
  acknowledgement is lost, so the sender retries and the sink dedupes it.
"""

from __future__ import annotations

import json
import os
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .errors import AckLostError, DeliveryFailedError
from .store import PendingOutput


@dataclass(frozen=True, slots=True)
class DeliveryResult:
    idem_id: str
    duplicate: bool


class LocalSink:
    def __init__(self, path: str | Path):
        self._path = Path(path)
        self._delivered: dict[str, dict[str, Any]] = {}
        self._fail_once_ids: set[str] = set()
        self._lose_ack_once_ids: set[str] = set()
        self._fail_count = 0
        self._ack_lost_count = 0
        self._duplicate_count = 0
        self._load()

    def _load(self) -> None:
        if self._path.exists():
            data = json.loads(self._path.read_text(encoding="utf-8"))
            self._delivered = dict(data.get("delivered", {}))
            self._fail_count = int(data.get("fail_count", 0))
            self._ack_lost_count = int(data.get("ack_lost_count", 0))
            self._duplicate_count = int(data.get("duplicate_count", 0))

    def _flush(self) -> None:
        self._path.parent.mkdir(parents=True, exist_ok=True)
        payload = {
            "delivered": self._delivered,
            "fail_count": self._fail_count,
            "ack_lost_count": self._ack_lost_count,
            "duplicate_count": self._duplicate_count,
        }
        fd, tmp_name = tempfile.mkstemp(
            dir=self._path.parent, prefix=self._path.name, suffix=".tmp"
        )
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                json.dump(payload, handle, sort_keys=True, indent=2)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(tmp_name, self._path)
            dir_fd = os.open(self._path.parent, os.O_RDONLY)
            try:
                os.fsync(dir_fd)
            finally:
                os.close(dir_fd)
        except BaseException:
            try:
                os.unlink(tmp_name)
            except FileNotFoundError:
                pass
            raise

    def fail_once(self, idem_id: str) -> None:
        """Make the next delivery attempt for ``idem_id`` fail."""
        self._fail_once_ids.add(idem_id)

    def lose_ack_once(self, idem_id: str) -> None:
        """Accept the payload durably but raise before acknowledging."""
        self._lose_ack_once_ids.add(idem_id)

    def deliver(
        self, pending: PendingOutput, delivered_at: str
    ) -> DeliveryResult:
        if pending.idem_id in self._fail_once_ids:
            self._fail_once_ids.discard(pending.idem_id)
            self._fail_count += 1
            self._flush()
            raise DeliveryFailedError(
                f"simulated delivery failure for {pending.idem_id}"
            )

        duplicate = pending.idem_id in self._delivered
        if duplicate:
            if self._delivered[pending.idem_id]["payload"] != pending.payload:
                raise AckLostError(
                    f"duplicate idem_id {pending.idem_id} with a different"
                    " payload; refusing to dedupe"
                )
            self._duplicate_count += 1
            self._flush()
            return DeliveryResult(idem_id=pending.idem_id, duplicate=True)

        self._delivered[pending.idem_id] = {
            "change_uid": pending.change_uid,
            "commit_seq": pending.commit_seq,
            "event_type": pending.event_type,
            "payload": pending.payload,
            "delivered_at": delivered_at,
        }
        self._flush()

        if pending.idem_id in self._lose_ack_once_ids:
            self._lose_ack_once_ids.discard(pending.idem_id)
            self._ack_lost_count += 1
            self._flush()
            raise AckLostError(
                f"simulated lost acknowledgement for {pending.idem_id}"
            )

        return DeliveryResult(idem_id=pending.idem_id, duplicate=False)

    def contains(self, idem_id: str) -> bool:
        return idem_id in self._delivered

    def __contains__(self, idem_id: object) -> bool:
        return isinstance(idem_id, str) and idem_id in self._delivered

    def __len__(self) -> int:
        return len(self._delivered)

    def stats(self) -> dict[str, int]:
        return {
            "unique_delivered": len(self._delivered),
            "duplicate_deliveries": self._duplicate_count,
            "failures": self._fail_count,
            "lost_acks": self._ack_lost_count,
        }
