"""Message, row and output types for the partitioned hash-join simulator.

Everything is plain data so it can be pickled into checkpoints and serialized
into replay logs. Keys may be None (SQL NULL); the NULL token below is what
hash indexes and filters use internally in place of None.
"""
from __future__ import annotations

from dataclasses import dataclass


class _NullToken:
    """Hashable singleton standing in for NULL keys inside hash indexes."""

    _instance = None

    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def __repr__(self):
        return "NULL"

    def __reduce__(self):  # keep the singleton across checkpoint pickling
        return (_null_token, ())


def _null_token():
    return NULL


NULL = _NullToken()


@dataclass(frozen=True)
class BuildData:
    """One build-side row. seq is monotonic per (partition, epoch) from 1."""
    partition: int
    epoch: int
    seq: int
    key: object      # hashable join key or None
    payload: object


@dataclass(frozen=True)
class BuildBarrier:
    """Declares that every seq <= last_seq has been sent for (partition, epoch).

    The partition (and therefore its dynamic filter) only becomes complete
    once all those messages have actually arrived and been applied.
    """
    partition: int
    epoch: int
    last_seq: int


@dataclass
class OutputBatch:
    """Stable-ID output batch. IDs are deterministic across crash/replay so
    the downstream test consumer can dedup redeliveries."""
    batch_id: str
    scan_batch_id: str
    rows: list
    created_at: int  # logical clock tick, scheduled by the local harness
