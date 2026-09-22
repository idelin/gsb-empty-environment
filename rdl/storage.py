"""Durable storage primitives.

Two append-only files live in each partition directory:

* ``spool.log`` - one JSON line per admitted event payload (the overflow
  target when memory is full); compacted below the acknowledgement watermark;
* ``wal.log``   - one JSON line per committed *state transition* (grants,
  revocations, acks, conflicts, resolutions, epoch anchors, migrations).

A line is only considered committed after ``flush + fsync``.  Both writers
support deterministic fault injection:

* a spool append can be failed (simulated disk failure during overflow) -
  the bytes are truncated again and ``SpillCommitError`` is raised;
* a WAL append can crash before the commit point - bytes are truncated and
  ``CommitCrash`` is raised, exactly modelling "ack lost before it lands".

Checkpoints atomically persist the full logical state to ``snapshot.json`` and
then truncate ``wal.log``, so neither file grows without bound.  Writes use
temp-file + ``os.replace`` + directory fsync.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any, Iterator


class CommitCrash(Exception):
    """Raised when a process "crashes" before a WAL commit became durable.

    The in-memory manager must be considered dead afterwards; recovery from
    disk rebuilds the state without the lost transition.
    """


class SpillCommitError(Exception):
    """Raised when a spool flush fails; the ingress is not admitted."""


# ---------------------------------------------------------------------------
# atomic file helpers
# ---------------------------------------------------------------------------


def fsync_dir(path: str | os.PathLike[str]) -> None:
    fd = os.open(os.fspath(path), os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def write_json_atomic(path: str | os.PathLike[str], obj: Any) -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(obj, fh, ensure_ascii=False, sort_keys=True)
        fh.write("\n")
        fh.flush()
        os.fsync(fh.fileno())
    os.replace(tmp, path)
    fsync_dir(path.parent)


def read_json(path: str | os.PathLike[str]) -> Any:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


# ---------------------------------------------------------------------------
# append-only JSON line file
# ---------------------------------------------------------------------------


class _LineLog:
    def __init__(self, path: str | os.PathLike[str]):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        if not self.path.exists():
            self.path.touch()

    def _append(self, record: dict, fail: bool) -> None:
        line = json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n"
        data = line.encode("utf-8")
        with open(self.path, "rb+") as fh:
            fh.seek(0, os.SEEK_END)
            offset = fh.tell()
            fh.write(data)
            fh.flush()
            os.fsync(fh.fileno())
            if fail:
                # Simulated fault after bytes left the process but before the
                # commit was acknowledged: undo the bytes deterministically.
                fh.truncate(offset)
                fh.flush()
                os.fsync(fh.fileno())
                return
        if not fail:
            return

    def iter_lines(self) -> Iterator[dict]:
        if not self.path.exists():
            return
        with open(self.path, "r", encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if line:
                    yield json.loads(line)

    def size(self) -> int:
        return self.path.stat().st_size if self.path.exists() else 0

    def truncate_all(self) -> None:
        with open(self.path, "wb"):
            pass
        fsync_dir(self.path.parent)


class Spool(_LineLog):
    """Event payload log."""

    def append_event(
        self,
        abs_index: int,
        epoch: int,
        seq: int,
        payload: Any,
        *,
        fail: bool = False,
    ) -> None:
        try:
            self._append(
                {"type": "EVENT", "abs": abs_index, "epoch": epoch,
                 "seq": seq, "payload": payload},
                fail=fail,
            )
        except OSError as exc:  # pragma: no cover - real disk failure path
            raise SpillCommitError(str(exc)) from exc
        if fail:
            raise SpillCommitError("injected spool flush failure")

    def iter_events(self) -> Iterator[dict]:
        yield from self.iter_lines()

    def compact(self, keep_min_abs: int) -> int:
        """Drop records strictly below ``keep_min_abs``; return removed count."""
        kept = [r for r in self.iter_events() if r["abs"] >= keep_min_abs]
        removed = sum(1 for r in self.iter_events() if r["abs"] < keep_min_abs)
        tmp = self.path.with_name(self.path.name + ".compact")
        with open(tmp, "w", encoding="utf-8") as fh:
            for rec in kept:
                fh.write(json.dumps(rec, ensure_ascii=False, sort_keys=True) + "\n")
            fh.flush()
            os.fsync(fh.fileno())
        os.replace(tmp, self.path)
        fsync_dir(self.path.parent)
        return removed


class WAL(_LineLog):
    """State-transition log."""

    def append_commit(self, record: dict, *, crash: bool = False) -> None:
        self._append(record, fail=crash)
        if crash:
            raise CommitCrash(
                f"crash before durable commit: {record.get('type')}"
            )


# ---------------------------------------------------------------------------
# fault injection
# ---------------------------------------------------------------------------


class FailureController:
    """Arms one-shot, deterministic faults for tests.

    * ``arm_spill_failure(partition, n)`` - next ``n`` spool appends fail;
    * ``arm_commit_crash(n)`` - next ``n`` WAL commits crash (bytes rolled
      back), modelling a process killed before the ack lands on disk.
    """

    def __init__(self) -> None:
        self._spill_fail_left: dict[str, int] = {}
        self._crash_left = 0

    def arm_spill_failure(self, partition: str, n: int = 1) -> None:
        self._spill_fail_left[partition] = self._spill_fail_left.get(partition, 0) + n

    def consume_spill_failure(self, partition: str) -> bool:
        left = self._spill_fail_left.get(partition, 0)
        if left <= 0:
            return False
        self._spill_fail_left[partition] = left - 1
        return True

    def arm_commit_crash(self, n: int = 1) -> None:
        self._crash_left += n

    def consume_commit_crash(self) -> bool:
        if self._crash_left <= 0:
            return False
        self._crash_left -= 1
        return True

    def disarm_all(self) -> None:
        self._spill_fail_left.clear()
        self._crash_left = 0


class WatermarkFile:
    """Tiny durable marker: the acknowledgement watermark of a partition.

    Written atomically alongside the spool, it lets recovery remember that
    every abs below the watermark was confirmed even if its payload record
    was never physically compacted (e.g. a crash landed between ack and
    compaction).
    """

    def __init__(self, path: str | os.PathLike[str]):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)

    def write(self, low_abs: int) -> None:
        write_json_atomic(self.path, {"low_abs": low_abs})

    def read(self) -> int | None:
        if not self.path.exists():
            return None
        return int(read_json(self.path)["low_abs"])
