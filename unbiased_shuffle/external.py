"""Resumable external-memory Fisher-Yates shuffle with crash recovery.

The record file holds ``n`` fixed-size records and is shuffled in place.
Two metadata files live next to it (in ``meta_dir``):

* ``checkpoint.json`` -- swap frontier, rng state, published positions
  (``published_from``: every position >= published_from is final), the
  caller's seed, the number of random samples used, and the swap trace.
* ``journal.json`` -- one pending swap: frontier, j, full before-images of
  both records, and the rng state right after the draw.

Write protocol per step (frontier i), in exactly this order:

1. draw j ~ Uniform[0, i]          (exactly one rng sample per step)
2. write the journal atomically (write tmp + fsync + rename)
3. apply the swap to the record store, flush and fsync
4. write the checkpoint atomically with frontier i-1
5. remove the journal (a crash before removal leaves a stale journal,
   which recovery detects via the frontier comparison and discards)

Recovery rules (journal.frontier vs checkpoint.frontier):

* equal              -> pending swap: re-apply the journal images (full-image
                        writes are idempotent), restore the rng state from
                        the journal, checkpoint frontier-1, continue;
* journal = cp + 1   -> stale journal from an already-checkpointed swap,
                        discard it;
* anything else      -> corruption, raise.

Because each swap is journaled with full before-images before any data
write can become durable, a crash never duplicates or loses a record, no
swap is ever re-drawn, and no swap is effectively applied twice: a
crash-recovered run performs exactly the (frontier, j) sequence of an
uninterrupted run with the same seed.

Random integer sampling is independent of the I/O block size and of
read/write retries: exactly one sample is drawn per frontier step, and
retries of failed I/O operations never touch the rng.
"""

from __future__ import annotations

import json
import os

CHECKPOINT_NAME = "checkpoint.json"
JOURNAL_NAME = "journal.json"


class CorruptionError(RuntimeError):
    """Raised when journal and checkpoint are mutually inconsistent."""


class SimulatedCrash(Exception):
    """Raised by crash hooks in tests/demos to simulate a power failure."""


def initialize_record_file(path: str, record_size: int, records) -> int:
    """Create a record file from an iterable of bytes; return record count."""
    records = [bytes(r) for r in records]
    for r in records:
        if len(r) != record_size:
            raise ValueError("all records must be exactly record_size bytes")
    with open(path, "wb") as fh:
        for r in records:
            fh.write(r)
        fh.flush()
        os.fsync(fh.fileno())
    return len(records)


def _atomic_write_json(path: str, obj) -> None:
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(obj, fh)
        fh.flush()
        os.fsync(fh.fileno())
    os.replace(tmp, path)
    _fsync_dir(os.path.dirname(path) or ".")


def _fsync_dir(dirpath: str) -> None:
    try:
        fd = os.open(dirpath, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    except OSError:
        pass
    finally:
        os.close(fd)


def _load_json(path: str):
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except FileNotFoundError:
        return None


class BlockedRecordStore:
    """Fixed-size record file with block I/O, write buffering and retries.

    ``block_size`` is the number of records per I/O block used for read
    caching and write coalescing; it only affects I/O granularity, never
    the random stream.  Failed low-level operations are retried up to
    ``max_retries`` times; retries do not consume randomness.  ``fault`` is
    an optional test hook called as ``fault(op, attempt)`` before every
    low-level attempt (``attempt`` is 0-based); it may raise ``OSError``
    to simulate transient I/O errors.

    The block cache is unbounded, which is fine for the record counts used
    in tests and demos; a production deployment would add eviction.
    """

    def __init__(self, path, record_size, nrecords, block_size=1,
                 max_retries=5, fault=None):
        if record_size <= 0 or nrecords < 0 or block_size <= 0:
            raise ValueError("bad store geometry")
        self.path = path
        self.record_size = record_size
        self.nrecords = nrecords
        self.block_size = block_size
        self.max_retries = max_retries
        self.fault = fault
        self.retries = 0
        self._fd = os.open(path, os.O_RDWR)
        self._blocks: dict[int, bytearray] = {}  # block index -> contents
        self._dirty: set[int] = set()

    # -- low-level I/O with retry -------------------------------------

    def _io(self, op, fn, *args):
        attempt = 0
        while True:
            try:
                if self.fault is not None:
                    self.fault(op, attempt)
                return fn(*args)
            except OSError:
                attempt += 1
                self.retries += 1
                if attempt > self.max_retries:
                    raise

    def _block_span(self, b):
        start_rec = b * self.block_size
        nrec = min(self.block_size, self.nrecords - start_rec)
        return start_rec * self.record_size, nrec * self.record_size

    def _pread_block(self, b):
        offset, size = self._block_span(b)
        data = os.pread(self._fd, size, offset)
        if len(data) != size:
            raise OSError("short read")
        return data

    def _pwrite_block(self, b, data):
        offset, _ = self._block_span(b)
        written = os.pwrite(self._fd, data, offset)
        if written != len(data):
            raise OSError("short write")

    def _fsync(self):
        os.fsync(self._fd)

    # -- record-level API ----------------------------------------------

    def _load_block(self, b):
        if b not in self._blocks:
            self._blocks[b] = bytearray(self._io("read", self._pread_block, b))
        return self._blocks[b]

    def read(self, pos) -> bytes:
        if not 0 <= pos < self.nrecords:
            raise IndexError(pos)
        b, within = divmod(pos, self.block_size)
        block = self._load_block(b)
        off = within * self.record_size
        return bytes(block[off:off + self.record_size])

    def write(self, pos, data: bytes) -> None:
        if not 0 <= pos < self.nrecords:
            raise IndexError(pos)
        if len(data) != self.record_size:
            raise ValueError("record size mismatch")
        b, within = divmod(pos, self.block_size)
        block = self._load_block(b)
        off = within * self.record_size
        block[off:off + self.record_size] = data
        self._dirty.add(b)

    def flush(self) -> None:
        """Make all buffered writes durable (used before each checkpoint)."""
        for b in sorted(self._dirty):
            self._io("write", self._pwrite_block, b, bytes(self._blocks[b]))
        self._dirty.clear()
        self._io("fsync", self._fsync)

    def read_all(self) -> list:
        return [self.read(i) for i in range(self.nrecords)]

    def close(self) -> None:
        if self._fd is not None:
            os.close(self._fd)
            self._fd = None

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()


class ExternalShuffler:
    """Fisher-Yates over a record file, resumable after crashes.

    The caller injects the random source (``rng``) and the ``seed`` it was
    built with; recovery validates that the checkpoint seed matches, then
    restores the rng state from disk.  ``crash_hook(phase, frontier)`` is a
    test/demo hook invoked at each protocol phase; it may raise
    ``SimulatedCrash``.
    """

    def __init__(self, store: BlockedRecordStore, rng, seed: int,
                 meta_dir: str, crash_hook=None):
        self.store = store
        self.rng = rng
        self.seed = seed
        self.meta_dir = meta_dir
        self.crash_hook = crash_hook or (lambda phase, frontier: None)
        self.trace: list[tuple[int, int]] = []
        self.samples = 0

    # -- paths and persistence ------------------------------------------

    @property
    def _checkpoint_path(self):
        return os.path.join(self.meta_dir, CHECKPOINT_NAME)

    @property
    def _journal_path(self):
        return os.path.join(self.meta_dir, JOURNAL_NAME)

    def _write_checkpoint(self, frontier: int) -> None:
        _atomic_write_json(self._checkpoint_path, {
            "version": 1,
            "seed": self.seed,
            "n": self.store.nrecords,
            "frontier": frontier,
            "published_from": frontier + 1,  # positions >= this are final
            "samples": self.samples,
            "rng_state": self.rng.getstate(),
            "trace": [list(t) for t in self.trace],
        })

    def _write_journal(self, i: int, j: int, record_i: bytes, record_j: bytes) -> None:
        _atomic_write_json(self._journal_path, {
            "version": 1,
            "seed": self.seed,
            "frontier": i,
            "j": j,
            "record_i": record_i.hex(),
            "record_j": record_j.hex(),
            "samples": self.samples,
            "rng_state": self.rng.getstate(),  # state right after the draw
        })

    # -- main loop --------------------------------------------------------

    def run(self) -> int:
        """Shuffle to completion (recovering first if needed)."""
        frontier = self._recover()
        while frontier >= 1:
            frontier = self._step(frontier)
        return 0

    def _step(self, i: int) -> int:
        self.crash_hook("before_draw", i)
        j = self.rng.randbelow(i + 1)  # exactly one sample per step
        self.samples += 1
        if not 0 <= j <= i:
            raise AssertionError(f"rng returned out-of-range index {j} for [0, {i}]")
        self.crash_hook("after_draw", i)
        record_i = self.store.read(i)
        record_j = self.store.read(j)
        self._write_journal(i, j, record_i, record_j)
        self.crash_hook("after_journal", i)
        self.store.write(i, record_j)
        self.store.write(j, record_i)
        self.store.flush()
        self.crash_hook("after_swap", i)
        self.trace.append((i, j))
        self._write_checkpoint(i - 1)
        self.crash_hook("after_checkpoint", i)
        self._remove_journal()  # swap fully checkpointed: journal no longer pending
        return i - 1

    def _remove_journal(self) -> None:
        try:
            os.remove(self._journal_path)
        except FileNotFoundError:
            pass

    # -- recovery ---------------------------------------------------------

    def _recover(self) -> int:
        cp = _load_json(self._checkpoint_path)
        jr = _load_json(self._journal_path)
        if cp is None:
            if jr is not None and jr["frontier"] != self.store.nrecords - 1:
                raise CorruptionError("journal without matching checkpoint")
            frontier = self.store.nrecords - 1
            self.trace = []
            self.samples = 0
        else:
            self._validate_checkpoint(cp)
            self.rng.setstate(cp["rng_state"])
            frontier = cp["frontier"]
            self.samples = cp["samples"]
            self.trace = [tuple(t) for t in cp["trace"]]
        if jr is not None:
            if jr["frontier"] == frontier:
                self._apply_pending_journal(jr)
                frontier -= 1
            elif jr["frontier"] == frontier + 1:
                self._remove_journal()  # stale: swap already checkpointed
            else:
                raise CorruptionError(
                    f"journal frontier {jr['frontier']} inconsistent with "
                    f"checkpoint frontier {frontier}")
        return frontier

    def _validate_checkpoint(self, cp) -> None:
        if cp.get("seed") != self.seed:
            raise CorruptionError("checkpoint seed does not match injected rng seed")
        if cp.get("n") != self.store.nrecords:
            raise CorruptionError("checkpoint record count mismatch")
        if cp.get("published_from") != cp.get("frontier") + 1:
            raise CorruptionError("checkpoint published positions inconsistent")

    def _apply_pending_journal(self, jr) -> None:
        i, j = jr["frontier"], jr["j"]
        # Full before-images make re-application idempotent: writing the
        # swapped images again is a no-op if the swap already landed.
        self.store.write(i, bytes.fromhex(jr["record_j"]))
        self.store.write(j, bytes.fromhex(jr["record_i"]))
        self.store.flush()
        self.rng.setstate(jr["rng_state"])
        self.samples = jr["samples"]
        self.trace.append((i, j))
        self._write_checkpoint(i - 1)
