import hashlib
import os
import shutil
import tempfile
from dataclasses import dataclass, field


class CancellationToken:
    def is_cancelled(self):
        return False


@dataclass
class MemoryBudget:
    limit_bytes: int
    used_bytes: int = 0
    peak_bytes: int = 0

    def __post_init__(self):
        if self.limit_bytes < 1:
            raise ValueError("memory limit must be positive")
        self.peak_bytes = max(self.peak_bytes, self.used_bytes)

    @property
    def available(self):
        return self.limit_bytes - self.used_bytes

    def acquire(self, amount):
        if amount < 0:
            raise ValueError("amount must be non-negative")
        self.used_bytes += amount
        self.peak_bytes = max(self.peak_bytes, self.used_bytes)

    def release(self, amount):
        if amount < 0 or amount > self.used_bytes:
            raise ValueError("invalid release amount")
        self.used_bytes -= amount


@dataclass
class AccountedBytes:
    physical_dictionary_bytes: int = 0
    physical_remap_bytes: int = 0
    physical_recoded_bytes: int = 0
    spill_bytes_written: int = 0
    spill_bytes_deleted: int = 0
    peak_memory_bytes: int = 0
    memory_limit_bytes: int = 0

    def as_dict(self):
        return {
            "physical_dictionary_bytes": self.physical_dictionary_bytes,
            "physical_remap_bytes": self.physical_remap_bytes,
            "physical_recoded_bytes": self.physical_recoded_bytes,
            "spill_bytes_written": self.spill_bytes_written,
            "spill_bytes_deleted": self.spill_bytes_deleted,
            "spill_bytes_remaining": self.spill_bytes_written - self.spill_bytes_deleted,
            "peak_memory_bytes": self.peak_memory_bytes,
            "memory_limit_bytes": self.memory_limit_bytes,
        }


@dataclass
class Ledger:
    accounting: AccountedBytes = field(default_factory=AccountedBytes)

    def add_write(self, amount, *, recoded=False, dictionary=False, remap=False):
        self.accounting.spill_bytes_written += amount
        if recoded:
            self.accounting.physical_recoded_bytes += amount
        if dictionary:
            self.accounting.physical_dictionary_bytes += amount
        if remap:
            self.accounting.physical_remap_bytes += amount

    def add_delete(self, amount):
        self.accounting.spill_bytes_deleted += amount

    def finalize_memory(self, budget):
        self.accounting.peak_memory_bytes = max(self.accounting.peak_memory_bytes, budget.peak_bytes)
        self.accounting.memory_limit_bytes = budget.limit_bytes


class ManagedWriter:
    def __init__(self, path, budget, ledger=None, *, buffer_size=4096, category="spill"):
        self.path = str(path)
        self.budget = budget
        self.ledger = ledger
        self.buffer_size = max(1, buffer_size)
        self.category = category
        self._file = open(self.path, "wb", buffering=0)
        self._buffer = bytearray()
        self._buffered_bytes = 0
        self.written_bytes = 0
        self.hasher = hashlib.sha256()

    def write(self, data):
        if not isinstance(data, (bytes, bytearray, memoryview)):
            raise TypeError("write requires bytes-like data")
        data = bytes(data)
        self.hasher.update(data)
        self.written_bytes += len(data)
        offset = 0
        while offset < len(data):
            room = self.buffer_size - len(self._buffer)
            piece = data[offset:offset + room]
            self.budget.acquire(len(piece))
            self._buffered_bytes += len(piece)
            self._buffer.extend(piece)
            offset += len(piece)
            if len(self._buffer) >= self.buffer_size:
                self.flush()

    def flush(self):
        if not self._buffer:
            return
        piece = bytes(self._buffer)
        self._file.write(piece)
        self._file.flush()
        if self.ledger is not None:
            self.ledger.add_write(
                len(piece),
                recoded=self.category == "recoded",
                dictionary=self.category == "dictionary",
                remap=self.category == "remap",
            )
        self._buffer.clear()
        self.budget.release(len(piece))
        self._buffered_bytes = 0

    def reserve(self, amount):
        self.budget.acquire(amount)

    def release(self, amount):
        self.budget.release(amount)

    def close(self):
        if self._file is None:
            return
        self.flush()
        self._file.close()
        self._file = None

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        self.close()


def ensure_dir(path):
    os.makedirs(path, exist_ok=True)


def fsync_dir(path):
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def atomic_replace(src, dst):
    os.replace(src, dst)


def atomic_write_bytes(path, data):
    path = str(path)
    directory = os.path.dirname(path) or "."
    ensure_dir(directory)
    fd, tmp_name = tempfile.mkstemp(prefix=".tmp-", dir=directory)
    try:
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_name, path)
        fsync_dir(directory)
    except BaseException:
        try:
            os.unlink(tmp_name)
        except FileNotFoundError:
            pass
        raise


def remove_accounted(path, ledger):
    size = os.path.getsize(path)
    os.unlink(path)
    ledger.add_delete(size)
    return size


def cleanup_dir(path):
    shutil.rmtree(path, ignore_errors=True)
