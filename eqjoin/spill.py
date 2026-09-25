"""Spillable partition: bounded in-memory buffer, overflows to disk."""
from __future__ import annotations

import os
import pickle


class SpillPartition:
    """Row buffer that spills pickled chunks to a file past a threshold.

    Items are opaque to this class. Re-iterable: rows() may be called
    multiple times (needed by the block-nested-loop fallback).
    """

    def __init__(self, path: str, spill_threshold: int):
        self.path = path
        self.threshold = max(1, spill_threshold)
        self.count = 0
        self.spilled = False
        self._buf = []
        self._file = None

    def add(self, item) -> None:
        self._buf.append(item)
        self.count += 1
        if len(self._buf) >= self.threshold:
            if not self.spilled:
                self._file = open(self.path, "wb")
                self.spilled = True
            self._flush()

    def _flush(self) -> None:
        if self._buf and self._file is not None:
            pickle.dump(self._buf, self._file, protocol=pickle.HIGHEST_PROTOCOL)
            self._buf.clear()

    def _close_writer(self) -> None:
        if self._file is not None:
            self._flush()
            self._file.close()
            self._file = None

    def rows(self):
        """Iterate all items, in insertion order."""
        if self.spilled:
            self._close_writer()
            with open(self.path, "rb") as fh:
                while True:
                    try:
                        chunk = pickle.load(fh)
                    except EOFError:
                        break
                    yield from chunk
            yield from self._buf  # normally empty after close; safety net
        else:
            yield from self._buf

    def dispose(self) -> None:
        self._close_writer()
        self._buf.clear()
        if self.spilled and os.path.exists(self.path):
            os.remove(self.path)
        self.spilled = False
