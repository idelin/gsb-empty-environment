"""Reference-counted, borrowable physical buffers.

Linearization points
--------------------
* ``acquire`` / ``release`` / ``borrow`` / ``_return_borrow`` each take the
  buffer's lock and perform their state transition atomically.
* Reclaim linearizes inside ``_maybe_reclaim_locked``: the instant both the
  reference count and the borrower count are zero, the buffer flips
  ``reclaimed`` and uncharges itself from the ledger, exactly once.
* A buffer is never reclaimed while borrowed, even if its last reference
  was released first; the pending reclaim runs when the final borrower
  returns its token.
"""

from __future__ import annotations

import itertools
import threading
from contextlib import contextmanager

from .errors import BorrowError, DoubleReleaseError, ResourceClosedError
from .ledger import Ledger


class Buffer:
    _id_counter = itertools.count(1)

    def __init__(self, data: bytearray | bytes, ledger: Ledger):
        self.id = next(Buffer._id_counter)
        self._data = bytearray(data)
        self.nbytes = len(self._data)
        self._ledger = ledger
        self._lock = threading.Lock()
        self._refs = 1
        self._borrowers = 0
        self._reclaimed = False
        # Charged at birth; may raise QuotaExceededError before the buffer
        # escapes to any caller, so a failed construction leaks nothing.
        ledger.charge(self.id, self.nbytes)

    # -- access ---------------------------------------------------------
    @property
    def data(self) -> bytearray:
        with self._lock:
            if self._reclaimed:
                raise ResourceClosedError(f"buffer {self.id} was reclaimed")
            return self._data

    @property
    def reclaimed(self) -> bool:
        with self._lock:
            return self._reclaimed

    def ref_count(self) -> int:
        with self._lock:
            return self._refs

    def borrower_count(self) -> int:
        with self._lock:
            return self._borrowers

    def is_shared(self) -> bool:
        with self._lock:
            return self._refs > 1

    # -- reference counting ----------------------------------------------
    def acquire(self) -> None:
        with self._lock:
            if self._reclaimed:
                raise ResourceClosedError(
                    f"buffer {self.id} was reclaimed; cannot acquire"
                )
            self._refs += 1

    def release(self) -> None:
        with self._lock:
            if self._refs <= 0:
                raise DoubleReleaseError(f"buffer {self.id} released twice")
            self._refs -= 1
            self._maybe_reclaim_locked()

    # -- borrowing (no reclaim while borrowed) ----------------------------
    def borrow(self) -> None:
        with self._lock:
            if self._reclaimed:
                raise ResourceClosedError(
                    f"buffer {self.id} was reclaimed; cannot borrow"
                )
            self._borrowers += 1

    def _return_borrow(self) -> None:
        with self._lock:
            if self._borrowers <= 0:
                raise BorrowError(f"buffer {self.id}: borrow returned twice")
            self._borrowers -= 1
            self._maybe_reclaim_locked()

    @contextmanager
    def borrowed(self):
        """Context manager pinning the buffer against reclaim."""
        self.borrow()
        try:
            yield self
        finally:
            self._return_borrow()

    # -- reclaim -----------------------------------------------------------
    def _maybe_reclaim_locked(self) -> None:
        """Linearization point of deallocation (called with lock held)."""
        if self._refs == 0 and self._borrowers == 0 and not self._reclaimed:
            self._reclaimed = True
            self._ledger.release(self.id)
