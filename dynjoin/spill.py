"""Spill buffer with memory and temp-disk quotas.

Rows waiting for an incomplete filter are buffered per partition: in memory
up to mem_quota_rows, then in per-partition temp files up to
disk_quota_bytes. When both quotas are exhausted try_add fails and the engine
applies backpressure (the whole scan batch is rejected and retried later);
rows are never dropped. Temp files are deleted as soon as they are drained.
"""
from __future__ import annotations

import os
import pickle
from collections import deque


class SpillBuffer:
    def __init__(self, tmpdir, mem_quota_rows, disk_quota_bytes):
        self.tmpdir = tmpdir
        os.makedirs(tmpdir, exist_ok=True)
        self.mem_quota_rows = mem_quota_rows
        self.disk_quota_bytes = disk_quota_bytes
        self.mem = {}       # partition -> deque[(batch_id, RowState)]
        self.mem_count = 0
        self.disk = {}      # partition -> [path, bytes]
        self.disk_used = 0
        self._handles = {}

    def try_add(self, partition, item):
        """Buffer one item. Returns a rollback token, or None on backpressure."""
        if self.mem_count < self.mem_quota_rows:
            self.mem.setdefault(partition, deque()).append(item)
            self.mem_count += 1
            return ("mem", partition)
        data = pickle.dumps(item)
        if self.disk_used + len(data) > self.disk_quota_bytes:
            return None
        entry = self.disk.setdefault(partition, [self._path(partition), 0])
        offset = entry[1]
        handle = self._handle(partition)
        handle.write(data)
        entry[1] += len(data)
        self.disk_used += len(data)
        return ("disk", partition, offset, len(data))

    def rollback(self, tokens):
        """Undo staged try_add calls (all-or-nothing scan batch accept)."""
        for tok in reversed(tokens):
            if tok[0] == "mem":
                self.mem[tok[1]].pop()
                self.mem_count -= 1
            else:
                _, partition, offset, length = tok
                self._handle(partition).truncate(offset)
                self.disk[partition][1] -= length
                self.disk_used -= length

    def requeue(self, partition, item):
        """Put back an item drained for the wrong epoch (quota-exempt)."""
        self.mem.setdefault(partition, deque()).append(item)
        self.mem_count += 1

    def drain(self, partition):
        """Remove and return every buffered item for a partition."""
        items = list(self.mem.pop(partition, ()))
        self.mem_count -= len(items)
        entry = self.disk.pop(partition, None)
        if entry is not None:
            path, size = entry
            self._close(partition)
            with open(path, "rb") as fh:
                while True:
                    try:
                        items.append(pickle.load(fh))
                    except EOFError:
                        break
            os.unlink(path)  # temp file released as soon as it has no users
            self.disk_used -= size
        return items

    def _path(self, partition):
        return os.path.join(self.tmpdir, f"spill-p{partition}.bin")

    def _handle(self, partition):
        handle = self._handles.get(partition)
        if handle is None:
            handle = open(self._path(partition), "a+b")
            self._handles[partition] = handle
        return handle

    def _close(self, partition):
        handle = self._handles.pop(partition, None)
        if handle is not None:
            handle.close()

    def flush(self):
        for handle in self._handles.values():
            handle.flush()

    def files(self):
        return [path for path, _ in self.disk.values() if os.path.exists(path)]

    def close(self):
        for partition in list(self._handles):
            self._close(partition)

    def __getstate__(self):
        # Checkpoint: mem rows travel inside the pickle and disk rows are
        # captured as bytes, so the checkpoint is atomic with respect to
        # post-checkpoint drains/appends of the temp files.
        self.flush()
        self.close()
        state = self.__dict__.copy()
        state["_handles"] = {}
        state["disk_data"] = {}
        for partition, (path, _size) in self.disk.items():
            with open(path, "rb") as fh:
                state["disk_data"][partition] = fh.read()
        return state

    def __setstate__(self, state):
        disk_data = state.pop("disk_data")
        self.__dict__.update(state)
        self._handles = {}
        self.disk_used = 0
        # write the checkpointed bytes back, atomically replacing whatever
        # post-checkpoint content the live files may have
        for partition, data in disk_data.items():
            path = self._path(partition)
            with open(path, "wb") as fh:
                fh.write(data)
            self.disk[partition] = [path, len(data)]
            self.disk_used += len(data)
        # remove orphaned temp files written after the checkpoint was taken
        if os.path.isdir(self.tmpdir):
            valid = {self._path(p) for p in self.disk}
            for name in os.listdir(self.tmpdir):
                path = os.path.join(self.tmpdir, name)
                if name.startswith("spill-p") and path not in valid:
                    os.unlink(path)
