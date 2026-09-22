"""Append-only, hash-chained event log used as the merger's only state.

Each record is one JSON line::

    {"seq": N, "prev": "<sha256>", "payload": {...}, "checksum": "<sha256>"}

``checksum`` covers ``seq``/``prev``/``payload``; ``prev`` chains to the
previous record's checksum, so any mutation, reordering or truncation of
history is detectable by the independent validator.  Appends are fsynced
before the merger acts on them (publish-then-deliver ordering), and a
torn final line left by a crash mid-write is tolerated on replay and
reported by the validator.
"""
from __future__ import annotations

import hashlib
import json
import os
from dataclasses import dataclass
from typing import Iterator, Optional

GENESIS = "0" * 64


def canonical(obj) -> str:
    return json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def record_checksum(seq: int, prev: str, payload: dict) -> str:
    return hashlib.sha256(
        canonical({"seq": seq, "prev": prev, "payload": payload}).encode("utf-8")
    ).hexdigest()


@dataclass
class ReadResult:
    records: list[dict]
    torn_tail: Optional[str]  # raw content of an incomplete final line, if any
    corruption: Optional[str]  # description of mid-log corruption, if any


class EventLog:
    def __init__(self, path: str):
        self.path = path
        self._seq = 0
        self._prev = GENESIS
        if os.path.exists(path):
            result = read_log(path)
            if result.corruption:
                raise CorruptLogError(result.corruption)
            for rec in result.records:
                self._seq = rec["seq"]
                self._prev = rec["checksum"]

    @property
    def seq(self) -> int:
        return self._seq

    @property
    def head(self) -> str:
        return self._prev

    def append(self, payload: dict) -> dict:
        seq = self._seq + 1
        rec = {
            "seq": seq,
            "prev": self._prev,
            "payload": payload,
            "checksum": record_checksum(seq, self._prev, payload),
        }
        line = canonical(rec) + "\n"
        with open(self.path, "a", encoding="utf-8") as fh:
            fh.write(line)
            fh.flush()
            os.fsync(fh.fileno())
        self._seq = seq
        self._prev = rec["checksum"]
        return rec

    def records(self) -> Iterator[dict]:
        yield from read_log(self.path).records


class CorruptLogError(RuntimeError):
    pass


def read_log(path: str) -> ReadResult:
    """Read and fully verify a log file.

    A malformed or mis-checksummed *final* line is reported as a torn
    tail (crash mid-write); the same problem anywhere earlier is
    corruption.
    """
    records: list[dict] = []
    torn_tail: Optional[str] = None
    corruption: Optional[str] = None
    if not os.path.exists(path):
        return ReadResult(records, None, None)
    with open(path, "r", encoding="utf-8") as fh:
        lines = fh.read().splitlines()
    prev = GENESIS
    for idx, line in enumerate(lines):
        last = idx == len(lines) - 1
        problem = _check_line(line, idx + 1, prev)
        if problem:
            if last:
                torn_tail = line
            else:
                corruption = f"line {idx + 1}: {problem}"
            break
        rec = json.loads(line)
        records.append(rec)
        prev = rec["checksum"]
    return ReadResult(records, torn_tail, corruption)


def _check_line(line: str, expect_seq: int, expect_prev: str) -> Optional[str]:
    try:
        rec = json.loads(line)
    except json.JSONDecodeError:
        return "not valid JSON (partial write?)"
    if not isinstance(rec, dict) or set(rec) != {"seq", "prev", "payload", "checksum"}:
        return "record shape invalid"
    if rec["seq"] != expect_seq:
        return f"seq gap: expected {expect_seq}, got {rec['seq']}"
    if rec["prev"] != expect_prev:
        return "hash chain broken"
    if rec["checksum"] != record_checksum(rec["seq"], rec["prev"], rec["payload"]):
        return "checksum mismatch"
    return None
