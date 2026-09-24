"""MVCC table + composite indexes with versioned reads and pinned snapshots.

Every mutation commits as a new monotonically increasing version.  Table rows
and index entries carry [begin, end) version ranges; a snapshot pins a version
and reads a consistent view of both table and all indexes at that version.
"""
from __future__ import annotations

from bisect import bisect_left, bisect_right

from .domains import encode_key


class SnapshotReclaimed(Exception):
    """Raised when reading with a snapshot id that has been released."""


class IndexEntry:
    __slots__ = ("key", "rowid", "begin", "end")

    def __init__(self, key, rowid, begin, end=None):
        self.key = key          # raw tuple of indexed column values
        self.rowid = rowid
        self.begin = begin
        self.end = end          # None = still live


class Index:
    def __init__(self, name, spec):
        self.name = name
        self.spec = tuple(spec)  # tuple[ColSpec]
        self.entries = []        # all entries ever, with version ranges
        self.live = {}           # rowid -> live IndexEntry
        self._views = {}         # version -> sorted [(encoded_key, rowid)]

    def key_of(self, values):
        return tuple(values[s.col] for s in self.spec)

    def insert_entry(self, raw_key, rowid, version):
        e = IndexEntry(raw_key, rowid, version)
        self.entries.append(e)
        self.live[rowid] = e

    def remove_entry(self, rowid, version):
        e = self.live.pop(rowid)
        e.end = version

    def view(self, version):
        """Sorted [(encoded_key, rowid)] visible at `version` (cached)."""
        v = self._views.get(version)
        if v is None:
            v = sorted(
                (
                    (encode_key(e.key, self.spec), e.rowid)
                    for e in self.entries
                    if e.begin <= version and (e.end is None or version < e.end)
                ),
                key=lambda t: (t[0], t[1]),
            )
            self._views[version] = v
        return v


def span_of(view, interval):
    """[start, end) offsets of `interval` inside a sorted index view."""
    if interval.lo_inc:
        s = bisect_left(view, interval.lo, key=lambda t: t[0])
    else:
        s = bisect_right(view, interval.lo, key=lambda t: t[0])
    if interval.hi_inc:
        e = bisect_right(view, interval.hi, key=lambda t: t[0])
    else:
        e = bisect_left(view, interval.hi, key=lambda t: t[0])
    return s, max(s, e)


class Database:
    def __init__(self, ncols):
        self.ncols = ncols
        self.version = 0
        self.rows = {}          # rowid -> list of [begin, end, values]
        self.indexes = {}       # name -> Index
        self.snapshots = {}     # sid -> pinned version
        self._next_rid = 1
        self._next_sid = 1

    # ---- schema ----
    def create_index(self, name, spec):
        idx = Index(name, spec)
        for rid, chain in self.rows.items():
            begin, end, vals = chain[-1]
            if end is None:
                idx.insert_entry(idx.key_of(vals), rid, begin)
        self.indexes[name] = idx
        return idx

    # ---- mutations (each commits a new version) ----
    def insert(self, values):
        assert len(values) == self.ncols
        self.version += 1
        v = self.version
        rid = self._next_rid
        self._next_rid += 1
        values = tuple(values)
        self.rows[rid] = [[v, None, values]]
        for idx in self.indexes.values():
            idx.insert_entry(idx.key_of(values), rid, v)
        return rid

    def update(self, rid, values):
        assert len(values) == self.ncols
        self.version += 1
        v = self.version
        chain = self.rows[rid]
        rec = chain[-1]
        assert rec[1] is None, "row already deleted"
        rec[1] = v
        values = tuple(values)
        chain.append([v, None, values])
        for idx in self.indexes.values():
            idx.remove_entry(rid, v)
            idx.insert_entry(idx.key_of(values), rid, v)

    def delete(self, rid):
        self.version += 1
        v = self.version
        rec = self.rows[rid][-1]
        assert rec[1] is None, "row already deleted"
        rec[1] = v
        for idx in self.indexes.values():
            idx.remove_entry(rid, v)

    # ---- versioned reads ----
    def get(self, rid, version):
        for begin, end, vals in self.rows.get(rid, ()):
            if begin <= version and (end is None or version < end):
                return vals
        return None

    def visible_rowids(self, version):
        return sorted(rid for rid in self.rows if self.get(rid, version) is not None)

    # ---- snapshots ----
    def begin_snapshot(self):
        sid = self._next_sid
        self._next_sid += 1
        self.snapshots[sid] = self.version
        return sid

    def snapshot_version(self, sid):
        try:
            return self.snapshots[sid]
        except KeyError:
            raise SnapshotReclaimed(f"snapshot {sid} has been released") from None

    def release_snapshot(self, sid):
        self.snapshots.pop(sid, None)

    def gc(self):
        """Purge row/index versions invisible to every pinned snapshot and to
        the current version."""
        horizon = min([self.version, *self.snapshots.values()])
        for chain in self.rows.values():
            chain[:] = [r for r in chain if r[1] is None or r[1] > horizon]
        for idx in self.indexes.values():
            idx.entries[:] = [
                e for e in idx.entries if e.end is None or e.end > horizon
            ]
            idx._views.clear()
