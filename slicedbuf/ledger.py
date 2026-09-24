"""Quota ledger: unique per-physical-buffer accounting with linearized ops."""
import threading
from collections import namedtuple


class ClosedError(RuntimeError):
    """Raised when accessing a released buffer or a closed ledger."""


class QuotaExceeded(RuntimeError):
    """Raised before any state change when a reservation exceeds the quota."""


class DoubleReleaseError(RuntimeError):
    """Raised when a buffer is released to the ledger more than once."""


LedgerSnapshot = namedtuple(
    "LedgerSnapshot",
    "live live_bytes peak_bytes freed_count quota closed",
)


class Ledger:
    """Accounts each physical buffer exactly once and enforces a byte quota.

    Linearization points: every mutation happens under one lock, so a
    reservation either fully succeeds or raises QuotaExceeded before any
    state changes; a release either debits exactly once or raises
    DoubleReleaseError. close() is idempotent; after close, new
    reservations are rejected while releases of still-live buffers remain
    legal so cleanup can complete.
    """

    def __init__(self, quota_bytes):
        if quota_bytes < 0:
            raise ValueError("quota must be >= 0")
        self._quota = quota_bytes
        self._lock = threading.Lock()
        self._live = {}
        self._freed = set()
        self._live_bytes = 0
        self._peak_bytes = 0
        self._closed = False

    def reserve(self, buffer_id, size):
        with self._lock:
            if self._closed:
                raise ClosedError("ledger is closed")
            if buffer_id in self._live:
                return
            if buffer_id in self._freed:
                raise DoubleReleaseError(f"buffer {buffer_id} was already freed")
            if self._live_bytes + size > self._quota:
                raise QuotaExceeded(
                    f"need {size}B, live {self._live_bytes}B, quota {self._quota}B"
                )
            self._live[buffer_id] = size
            self._live_bytes += size
            if self._live_bytes > self._peak_bytes:
                self._peak_bytes = self._live_bytes

    def release(self, buffer_id):
        with self._lock:
            size = self._live.pop(buffer_id, None)
            if size is None:
                raise DoubleReleaseError(f"buffer {buffer_id} is not live")
            self._freed.add(buffer_id)
            self._live_bytes -= size

    def close(self):
        with self._lock:
            self._closed = True

    def snapshot(self):
        with self._lock:
            return LedgerSnapshot(
                live=dict(self._live),
                live_bytes=self._live_bytes,
                peak_bytes=self._peak_bytes,
                freed_count=len(self._freed),
                quota=self._quota,
                closed=self._closed,
            )
