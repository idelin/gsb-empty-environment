"""Physical buffers with refcounts, borrow leases and linearized reclaim."""
import itertools
import threading

from .ledger import ClosedError, DoubleReleaseError

_ids = itertools.count(1)


class Buffer:
    """A physical byte buffer uniquely accounted in a Ledger.

    Contract: a buffer is reclaimed at exactly one linearization point, the
    release()/unborrow that drops refs and borrows to zero inside the buffer
    lock; the ledger is debited in the same critical section. It is never
    reclaimed while borrowed and never freed twice. Data access after
    reclaim raises ClosedError.
    """

    def __init__(self, ledger, data):
        self.ledger = ledger
        self.id = next(_ids)
        self._data = bytearray(data)
        self.size = len(self._data)
        self._lock = threading.Lock()
        self._refs = 1
        self._borrows = 0
        self._state = "open"
        ledger.reserve(self.id, self.size)

    def _check_open(self):
        if self._state != "open":
            raise ClosedError(f"buffer {self.id} is released")

    def addref(self):
        with self._lock:
            self._check_open()
            if self._refs == 0:
                raise ClosedError(f"buffer {self.id} is drained")
            self._refs += 1

    def release(self):
        with self._lock:
            if self._state != "open":
                return
            if self._refs <= 0:
                raise DoubleReleaseError(f"buffer {self.id} has no refs to drop")
            self._refs -= 1
            if self._refs == 0 and self._borrows == 0:
                self._reclaim_locked()

    def borrow(self):
        with self._lock:
            self._check_open()
            self._borrows += 1
        return _Borrow(self)

    def _end_borrow(self):
        with self._lock:
            self._borrows -= 1
            if self._refs == 0 and self._borrows == 0:
                self._reclaim_locked()

    def _reclaim_locked(self):
        self._state = "released"
        self.ledger.release(self.id)

    def read(self, offset, length):
        with self._lock:
            self._check_open()
            return bytes(self._data[offset:offset + length])

    def write(self, offset, data):
        with self._lock:
            self._check_open()
            self._data[offset:offset + len(data)] = data

    def is_shared(self):
        with self._lock:
            self._check_open()
            return self._refs > 1 or self._borrows > 0

    @property
    def is_open(self):
        with self._lock:
            return self._state == "open"


class _Borrow:
    """Idempotent borrow lease; the buffer cannot be reclaimed while held."""

    def __init__(self, buffer):
        self._buffer = buffer
        self._closed = False

    def close(self):
        if self._closed:
            return
        self._closed = True
        self._buffer._end_borrow()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False
