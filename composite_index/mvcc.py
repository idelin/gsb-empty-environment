"""MVCC storage: versioned table and versioned composite indexes.

Every mutation bumps a global commit version and appends to per-row /
per-key version chains.  Readers pin a snapshot version and always see a
consistent view: updating an indexed key adds a new version of the new key
and a tombstone on the old key, so an old cursor still finds the row under
its old key.  Chains are never rewritten in place.
"""
from __future__ import annotations

import functools

from .core import NULL, compare_keys


class Mvcc:
    def __init__(self):
        self.version = 0

    def bump(self) -> int:
        self.version += 1
        return self.version


def normalize_row(row: dict) -> dict:
    return {k: (NULL if v is None else v) for k, v in row.items()}


class Table:
    def __init__(self, columns):
        self.columns = tuple(columns)
        self.rows = {}  # rid -> [(version, row_dict | None)], ascending
        self._next_rid = 1

    def read(self, rid, version):
        chain = self.rows.get(rid)
        if not chain:
            return None
        for ver, row in reversed(chain):
            if ver <= version:
                return row
        return None

    def insert(self, row, version) -> int:
        rid = self._next_rid
        self._next_rid += 1
        self.rows[rid] = [(version, row)]
        return rid

    def update(self, rid, row, version):
        self.rows[rid].append((version, row))

    def delete(self, rid, version):
        self.rows[rid].append((version, None))

    def count(self, version) -> int:
        return sum(1 for rid in self.rows if self.read(rid, version) is not None)


class Index:
    """A composite index: key tuple -> rid -> version chain of presence."""

    def __init__(self, name, specs):
        self.name = name
        self.specs = tuple(specs)
        self.entries = {}  # key tuple -> {rid: [(version, present)]}
        self._cache = {}   # version -> sorted [(key, (rid, ...))]

    def key_of(self, row: dict):
        return tuple(row.get(s.name, NULL) for s in self.specs)

    def add(self, key, rid, version):
        self.entries.setdefault(key, {}).setdefault(rid, []).append((version, True))
        self._cache.clear()

    def remove(self, key, rid, version):
        self.entries.setdefault(key, {}).setdefault(rid, []).append((version, False))
        self._cache.clear()

    def _present(self, chain, version) -> bool:
        for ver, flag in reversed(chain):
            if ver <= version:
                return flag
        return False

    def visible(self, version):
        """All (key, rids) live at ``version``, sorted in index order."""
        cached = self._cache.get(version)
        if cached is not None:
            return cached
        out = []
        for key, rids in self.entries.items():
            live = tuple(sorted(rid for rid, chain in rids.items()
                                if self._present(chain, version)))
            if live:
                out.append((key, live))
        out.sort(key=functools.cmp_to_key(
            lambda a, b: compare_keys(self.specs, a[0], b[0])))
        self._cache[version] = out
        return out

    def count_in_interval(self, interval, version) -> int:
        return sum(len(rids) for key, rids in self.visible(version)
                   if interval.contains(self.specs, key))
