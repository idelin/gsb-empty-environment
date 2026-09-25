import heapq
import os
import struct
import tempfile

from .errors import MergeCancelled
from .spilling import ManagedWriter, ensure_dir, fsync_dir

RUN_MAGIC = b"SOR1"
_HEADER = struct.Struct(">4sIQ")
_RECORD_HEADER = struct.Struct(">II")


class _RunReader:
    def __init__(self, path):
        self.path = str(path)
        self.handle = open(self.path, "rb", buffering=0)
        magic, self.key_length, self.count = _HEADER.unpack(self.handle.read(_HEADER.size))
        if magic != RUN_MAGIC:
            raise ValueError("invalid external sort run")
        self.seen = 0

    def read_record(self):
        if self.seen >= self.count:
            return None
        key_len, payload_len = _RECORD_HEADER.unpack(self.handle.read(_RECORD_HEADER.size))
        key = self.handle.read(key_len)
        payload = self.handle.read(payload_len)
        if len(key) != key_len or len(payload) != payload_len:
            raise EOFError("truncated external sort run")
        self.seen += 1
        return key, payload

    def close(self):
        self.handle.close()


class ExternalSorter:
    """Fixed-key external sorter using only standard-library file I/O and heapq."""

    def __init__(self, run_dir, budget, ledger, *, key_length, run_prefix="run-", buffer_size=4096):
        self.run_dir = str(run_dir)
        self.budget = budget
        self.ledger = ledger
        self.key_length = key_length
        self.run_prefix = run_prefix
        self.buffer_size = buffer_size
        self.records = []
        self.record_bytes = 0
        self.runs = []
        self.intermediate_runs = []
        ensure_dir(self.run_dir)

    def add_record(self, key, payload):
        if self.key_length and len(key) != self.key_length:
            raise ValueError("sort key has incorrect length")
        record_size = len(key) + len(payload)
        if self.records and self.record_bytes + record_size > self.budget.limit_bytes:
            self.flush()
        self.budget.acquire(record_size)
        self.records.append((key, payload))
        self.record_bytes += record_size

    def check_cancelled(self, cancellation_token):
        if cancellation_token is not None and cancellation_token.is_cancelled():
            raise MergeCancelled("merge cancelled during external sort")

    def flush(self, cancellation_token=None):
        if not self.records:
            return None
        self.check_cancelled(cancellation_token)
        self.records.sort(key=lambda record: record[0])
        fd, path = tempfile.mkstemp(prefix=self.run_prefix, suffix=".run", dir=self.run_dir)
        os.close(fd)
        with ManagedWriter(
            path,
            self.budget,
            self.ledger,
            buffer_size=self.buffer_size,
        ) as writer:
            writer.write(_HEADER.pack(RUN_MAGIC, self.key_length, len(self.records)))
            for key, payload in self.records:
                writer.write(_RECORD_HEADER.pack(len(key), len(payload)))
                writer.write(key)
                writer.write(payload)
        fsync_dir(self.run_dir)
        self.runs.append(path)
        self.budget.release(self.record_bytes)
        self.records.clear()
        self.record_bytes = 0
        return path

    def _merge_group_to_run(self, paths, cancellation_token):
        self.check_cancelled(cancellation_token)
        readers = [_RunReader(path) for path in paths]
        fd, output_path = tempfile.mkstemp(prefix=self.run_prefix + "level-", suffix=".run", dir=self.run_dir)
        os.close(fd)
        key_length = readers[0].key_length
        from .spilling import ManagedWriter

        count = 0
        with ManagedWriter(output_path, self.budget, self.ledger, buffer_size=self.buffer_size) as writer:
            writer.write(_HEADER.pack(RUN_MAGIC, key_length, 0))
            heap = []
            for index, reader in enumerate(readers):
                record = reader.read_record()
                if record is not None:
                    heap.append((record[0], index, record[1]))
            heapq.heapify(heap)
            while heap:
                key, index, payload = heapq.heappop(heap)
                writer.write(_RECORD_HEADER.pack(len(key), len(payload)))
                writer.write(key)
                writer.write(payload)
                count += 1
                record = readers[index].read_record()
                if record is not None:
                    heapq.heappush(heap, (record[0], index, record[1]))
                if count % 4096 == 0:
                    self.check_cancelled(cancellation_token)
        for reader in readers:
            reader.close()
        with open(output_path, "r+b", buffering=0) as handle:
            handle.seek(8)
            handle.write(struct.pack(">Q", count))
        fsync_dir(self.run_dir)
        self.intermediate_runs.append(output_path)
        return output_path

    def iter_merged(self, cancellation_token=None):
        if self.records:
            self.flush(cancellation_token)
        if not self.runs:
            return
        fan_in = max(2, min(8, max(2, self.budget.limit_bytes // 64)))
        level_paths = list(self.runs)
        while len(level_paths) > fan_in:
            next_level = []
            for start in range(0, len(level_paths), fan_in):
                group = level_paths[start:start + fan_in]
                if len(group) == 1:
                    next_level.append(group[0])
                else:
                    next_level.append(self._merge_group_to_run(group, cancellation_token))
            level_paths = next_level
        readers = [_RunReader(path) for path in level_paths]
        heap = []
        for index, reader in enumerate(readers):
            record = reader.read_record()
            if record is not None:
                heap.append((record[0], index, record[1]))
        heapq.heapify(heap)
        checks = 0
        try:
            while heap:
                key, index, payload = heapq.heappop(heap)
                yield key, payload
                record = readers[index].read_record()
                if record is not None:
                    heapq.heappush(heap, (record[0], index, record[1]))
                checks += 1
                if cancellation_token is not None and checks % 4096 == 0:
                    self.check_cancelled(cancellation_token)
        finally:
            for reader in readers:
                reader.close()

    def cleanup(self):
        for path in [*self.runs, *self.intermediate_runs]:
            try:
                size = os.path.getsize(path)
                os.unlink(path)
                self.ledger.add_delete(size)
            except FileNotFoundError:
                pass
        if self.record_bytes:
            self.budget.release(self.record_bytes)
        self.records.clear()
        self.record_bytes = 0
        self.runs.clear()
        self.intermediate_runs.clear()
