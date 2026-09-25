"""Reference-counted tensor storage with an explicit lifecycle.

A Storage holds the flat data buffer of a Tensor.  Graphs pin the storages
of every tensor they save for backward; a storage may only be reclaimed
once its pin count drops to zero.  Releasing one graph therefore never
reclaims storage that is still referenced by another, independent graph.
"""

import threading


class Storage:
    __slots__ = ("data", "_pins", "_freed", "_lock")

    def __init__(self, data):
        self.data = data
        self._pins = 1  # owned by the creating tensor
        self._freed = False
        self._lock = threading.Lock()

    def pin(self):
        with self._lock:
            if self._freed:
                raise RuntimeError("cannot pin freed storage")
            self._pins += 1

    def unpin(self):
        with self._lock:
            self._pins -= 1
            if self._pins == 0:
                self._freed = True
                self.data = None

    @property
    def pins(self):
        return self._pins

    @property
    def is_freed(self):
        return self._freed
