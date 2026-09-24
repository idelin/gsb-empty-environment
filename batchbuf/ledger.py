"""Physical-buffer byte accounting with an optional quota.

The ledger charges *physical* buffers exactly once, keyed by buffer id.
Ten slices sharing one buffer cost the buffer's bytes one time.  Temporary
copies (e.g. the private copy made during copy-on-write) are ordinary
buffers and are charged while they live, so the ledger's peak reflects
them.

All mutations happen under a single lock; each charge/release is a
linearizable operation.  Quota checks run before any state is mutated, so
an over-quota request is rejected atomically with no side effects.
"""

from __future__ import annotations

import threading

from .errors import DoubleReleaseError, QuotaExceededError


class Ledger:
    def __init__(self, quota: int | None = None):
        if quota is not None and quota < 0:
            raise ValueError("quota must be non-negative or None")
        self._quota = quota
        self._lock = threading.Lock()
        self._used = 0
        self._peak = 0
        self._live: dict[int, int] = {}  # buffer id -> nbytes

    @property
    def quota(self) -> int | None:
        return self._quota

    def set_quota(self, quota: int | None) -> None:
        """Adjust the quota.  Lowering it below current usage is allowed;
        only new charges are then rejected."""
        if quota is not None and quota < 0:
            raise ValueError("quota must be non-negative or None")
        with self._lock:
            self._quota = quota

    @property
    def used(self) -> int:
        with self._lock:
            return self._used

    @property
    def peak(self) -> int:
        with self._lock:
            return self._peak

    def live_buffers(self) -> dict[int, int]:
        """Snapshot of {buffer id: nbytes} currently charged."""
        with self._lock:
            return dict(self._live)

    def charge_many(self, items) -> None:
        """Atomically charge several physical buffers.

        Ids already live are skipped (shared buffers count once).  If the
        total *new* bytes would exceed the quota, QuotaExceededError is
        raised before anything is recorded.
        """
        entries = list(items)
        with self._lock:
            fresh = [(bid, n) for bid, n in entries if bid not in self._live]
            total = sum(n for _, n in fresh)
            if self._quota is not None and self._used + total > self._quota:
                raise QuotaExceededError(
                    f"charge of {total} bytes would exceed quota "
                    f"{self._quota} (used={self._used})"
                )
            for bid, n in fresh:
                self._live[bid] = n
            self._used += total
            if self._used > self._peak:
                self._peak = self._used

    def charge(self, buffer_id: int, nbytes: int) -> None:
        self.charge_many([(buffer_id, nbytes)])

    def release(self, buffer_id: int) -> int:
        """Remove a buffer's charge.  Releasing twice is an error."""
        with self._lock:
            nbytes = self._live.pop(buffer_id, None)
            if nbytes is None:
                raise DoubleReleaseError(
                    f"buffer {buffer_id} is not charged to this ledger"
                )
            self._used -= nbytes
            return nbytes
