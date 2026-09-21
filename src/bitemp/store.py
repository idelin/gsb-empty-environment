"""Append-only version store: the source of truth.

The store never rewrites or deletes history.  Corrections, retractions and
backfills are appended as new versions; the index is a derived structure
that can always be rebuilt from the store.
"""

from __future__ import annotations

from typing import Any, Iterable

from .model import Kind, Version


class VersionStore:
    def __init__(self, versions: Iterable[Version] = ()) -> None:
        self._versions: list[Version] = []
        self._next_seq: dict[str, int] = {}
        self._used_seqs: set[tuple[str, int]] = set()
        for v in versions:
            self.append(v)

    def record(
        self,
        object_id: str,
        valid_from: float,
        valid_to: float,
        tx_from: float,
        kind: Kind = Kind.ASSERT,
        value: Any = None,
        seq: int | None = None,
    ) -> Version:
        if seq is None:
            seq = self._next_seq.get(object_id, 0) + 1
        v = Version(object_id, valid_from, valid_to, tx_from, seq, kind, value)
        self.append(v)
        return v

    def append(self, v: Version) -> Version:
        key = (v.object_id, v.seq)
        if key in self._used_seqs:
            raise ValueError(f"duplicate seq {v.seq} for object {v.object_id!r}")
        self._used_seqs.add(key)
        self._next_seq[v.object_id] = max(
            v.seq, self._next_seq.get(v.object_id, 0)
        )
        self._versions.append(v)
        return v

    @property
    def versions(self) -> tuple[Version, ...]:
        return tuple(self._versions)
