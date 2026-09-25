"""Heap-allocated flat float buffers with explicit reference counting.

A :class:`Storage` models one buffer owned by the fake heap.  Tensors acquire a
reference when they start sharing a buffer and release it when they die or when
a graph explicitly drops them.  A buffer is only recycled once **every**
reference is gone, which is what keeps storage shared between two graphs alive
after one of the graphs is released.
"""

from __future__ import annotations

import threading

from .errors import StorageFreedError


class Storage:
    __slots__ = ("id", "data", "_refcount", "_alive", "_lock")

    _next_id = 0
    _id_lock = threading.Lock()

    def __init__(self, data):
        with Storage._id_lock:
            Storage._next_id += 1
            self.id = Storage._next_id
        self.data = list(data)
        self._refcount = 0
        self._alive = True
        self._lock = threading.Lock()

    def retain(self):
        """Take one reference; fails on use-after-free."""
        with self._lock:
            if not self._alive:
                raise StorageFreedError(
                    f"storage {self.id} has already been recycled"
                )
            self._refcount += 1

    def release(self):
        """Drop one reference.  Returns True if this call recycled the buffer."""
        with self._lock:
            if not self._alive:
                return False
            self._refcount -= 1
            if self._refcount <= 0:
                self.data = None
                self._alive = False
                return True
            return False

    @property
    def refcount(self):
        return self._refcount

    @property
    def alive(self):
        return self._alive

    def __repr__(self):
        state = "alive" if self._alive else "freed"
        return f"Storage(id={self.id}, refs={self._refcount}, {state})"
