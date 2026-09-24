"""Grouped, retractable Top-K engine.

Per group the engine keeps a small in-memory window of the best candidate
keys; overflow is spilled to immutable sorted segments in a BlockStore.
Deletions are backfilled from the in-memory buffer first; external segments
are merged in only when their min-key overlaps the current top window, so
backfill never scans a whole group. Every commit bumps a version and emits
a stable-id delta; snapshots pin a version for repeatable pagination.
"""
import bisect
import os
import pickle

from .keys import build_key, key_values
from .storage import BlockStore


class Snapshot:
    """Read-only view of all group tops pinned at one engine version."""

    def __init__(self, engine, version):
        self._engine = engine
        self.version = version

    def page(self, group, offset=0, limit=None):
        top = self._engine._top_at(group, self.version)
        end = None if limit is None else offset + limit
        return list(top[offset:end])

    def ids(self, group):
        return [pk for pk, _ in self.page(group)]


class GroupedTopK:
    def __init__(self, name, k, sort_spec, group_field, directory,
                 buffer_size=8, spill_slack=8):
        if k < 0:
            raise ValueError("k must be >= 0")
        self.name = name
        self.k = k
        self.sort_spec = tuple(tuple(f) for f in sort_spec)
        self.group_field = group_field
        self.buffer_size = max(1, buffer_size)
        self.spill_slack = max(1, spill_slack)
        os.makedirs(directory, exist_ok=True)
        self.store = BlockStore(directory)
        self._state_file = os.path.join(directory, "state.pkl")
        self.version = 0
        self._records = {}     # pk -> record dict
        self._id_index = {}    # pk -> (group, key); single source of truth, no dupes
        self._mem = {}         # group -> sorted list of keys (best first)
        self._segments = {}    # group -> [[min_key, max_key, bid, size], ...]
        self._tombstones = {}  # group -> set of pks deleted while inside a segment
        self._top = {}         # group -> tuple of (pk, values), current top-k
        self._top_log = {}     # group -> [(version, top tuple)], for snapshots
        self.commit_log = []

    # ------------------------------------------------------------------ #
    # mutation
    # ------------------------------------------------------------------ #
    def apply_change(self, pk, before, after):
        """Apply one row change; returns the set of affected groups."""
        groups = set()
        if before is not None or pk in self._id_index:
            old = self._remove(pk)
            if old is not None:
                groups.add(old)
        if after is not None:
            groups.add(self._insert(pk, after[self.group_field], after))
        return groups

    def commit(self, changes, commit_seq=None):
        """Apply one transaction's changes, bump version, emit id delta."""
        affected = set()
        for pk, before, after in changes:
            affected |= self.apply_change(pk, before, after)
        self.version += 1
        delta = {}
        for group in sorted(affected, key=repr):
            new_top = self._top.get(group, ())
            log = self._top_log.setdefault(group, [])
            old_top = log[-1][1] if log else ()
            if new_top != old_top:
                log.append((self.version, new_top))
                old_ids = [pk for pk, _ in old_top]
                new_ids = [pk for pk, _ in new_top]
                delta[group] = {
                    "added": [i for i in new_ids if i not in old_ids],
                    "removed": [i for i in old_ids if i not in new_ids],
                }
        entry = {"version": self.version, "commit_seq": commit_seq,
                 "table": self.name, "groups": delta}
        self.commit_log.append(entry)
        return entry

    def _insert(self, pk, group, record):
        key = build_key(self.sort_spec, record, pk)
        self._id_index[pk] = (group, key)
        self._records[pk] = record
        self._tombstones.setdefault(group, set()).discard(pk)
        bisect.insort(self._mem.setdefault(group, []), key)
        self._maintain(group)
        return group

    def _remove(self, pk):
        entry = self._id_index.pop(pk, None)
        if entry is None:
            return None
        group, key = entry
        self._records.pop(pk, None)
        mem = self._mem.setdefault(group, [])
        i = bisect.bisect_left(mem, key)
        if i < len(mem) and mem[i] == key:
            mem.pop(i)
        else:
            # The key lives in an external segment: tombstone it lazily so
            # deletes stay O(1) block accesses instead of rewriting segments.
            self._tombstones.setdefault(group, set()).add(pk)
        self._maintain(group)
        return group

    # ------------------------------------------------------------------ #
    # candidate window maintenance
    # ------------------------------------------------------------------ #
    def _maintain(self, group):
        mem = self._mem.setdefault(group, [])
        tombs = self._tombstones.setdefault(group, set())
        segs = self._segments.setdefault(group, [])
        keep = self.k + self.buffer_size
        if tombs and any(k[-1] in tombs for k in mem):
            mem[:] = [k for k in mem if k[-1] not in tombs]
        # Merge external segments only while one can still hold a key that
        # belongs inside the current top window (bounded, never a full scan).
        while segs and self.k:
            if len(mem) < self.k:
                need = True
            else:
                need = min(s[0] for s in segs) < mem[self.k - 1]
            if not need:
                break
            idx = min(range(len(segs)), key=lambda i: segs[i][0])
            _, _, bid, _ = segs.pop(idx)
            entries = self.store.read_segment(bid)
            self.store.drop_segment(bid)
            mem.extend(e for e in entries if e[-1] not in tombs)
            mem.sort()
            while len(mem) > keep + self.spill_slack:
                self._spill(mem, segs, keep)
        while len(mem) > keep + self.spill_slack:
            self._spill(mem, segs, keep)
        if not segs and tombs:
            tombs.clear()
        self._refresh_top(group)

    def _spill(self, mem, segs, keep):
        chunk = mem[keep:]
        del mem[keep:]
        if chunk:
            bid = self.store.write_segment(chunk)
            segs.append([chunk[0], chunk[-1], bid, len(chunk)])

    def _refresh_top(self, group):
        if self.k:
            mem = self._mem.get(group, [])
            top = tuple((k[-1], self._values_of(k[-1])) for k in mem[:self.k])
        else:
            top = ()
        self._top[group] = top

    def _values_of(self, pk):
        return key_values(self.sort_spec, self._records[pk])

    # ------------------------------------------------------------------ #
    # reads
    # ------------------------------------------------------------------ #
    def top(self, group):
        return list(self._top.get(group, ()))

    def top_ids(self, group):
        return [pk for pk, _ in self._top.get(group, ())]

    def snapshot(self):
        return Snapshot(self, self.version)

    def _top_at(self, group, version):
        log = self._top_log.get(group, [])
        versions = [v for v, _ in log]
        i = bisect.bisect_right(versions, version) - 1
        return log[i][1] if i >= 0 else ()

    def candidate_count(self, group):
        """Live candidate count (in-memory window + external segments)."""
        mem = self._mem.get(group, [])
        segs = self._segments.get(group, [])
        tombs = self._tombstones.get(group, set())
        return len(mem) + sum(s[3] for s in segs) - len(tombs)

    # ------------------------------------------------------------------ #
    # persistence / recovery
    # ------------------------------------------------------------------ #
    def checkpoint(self):
        state = {
            "name": self.name, "k": self.k, "sort_spec": self.sort_spec,
            "group_field": self.group_field,
            "buffer_size": self.buffer_size, "spill_slack": self.spill_slack,
            "version": self.version, "records": self._records,
            "id_index": self._id_index, "mem": self._mem,
            "segments": self._segments, "tombstones": self._tombstones,
            "top": self._top, "top_log": self._top_log,
            "commit_log": self.commit_log,
            "next_block": self.store.next_block,
            "reads": self.store.reads, "writes": self.store.writes,
        }
        with open(self._state_file, "wb") as fh:
            pickle.dump(state, fh)

    @classmethod
    def recover(cls, directory):
        with open(os.path.join(directory, "state.pkl"), "rb") as fh:
            st = pickle.load(fh)
        obj = cls.__new__(cls)
        obj.name = st["name"]
        obj.k = st["k"]
        obj.sort_spec = st["sort_spec"]
        obj.group_field = st["group_field"]
        obj.buffer_size = st["buffer_size"]
        obj.spill_slack = st["spill_slack"]
        obj.store = BlockStore(directory)
        obj.store.next_block = st["next_block"]
        obj.store.reads = st["reads"]
        obj.store.writes = st["writes"]
        obj._state_file = os.path.join(directory, "state.pkl")
        obj.version = st["version"]
        obj._records = st["records"]
        obj._id_index = st["id_index"]
        obj._mem = st["mem"]
        obj._segments = st["segments"]
        obj._tombstones = st["tombstones"]
        obj._top = st["top"]
        obj._top_log = st["top_log"]
        obj.commit_log = st["commit_log"]
        return obj


class Pipeline:
    """Routes local change events (txn/table/pk/before/after/seq) to engines."""

    def __init__(self, engines):
        self.engines = dict(engines)

    def apply_events(self, events):
        by_seq = {}
        for ev in events:
            by_seq.setdefault(ev["commit_seq"], []).append(ev)
        deltas = []
        for seq in sorted(by_seq):
            per_table = {}
            for ev in by_seq[seq]:
                per_table.setdefault(ev["table"], []).append(ev)
            for table in sorted(per_table):
                engine = self.engines.get(table)
                if engine is None:
                    continue
                changes = [(e["pk"], e.get("before"), e.get("after"))
                           for e in per_table[table]]
                deltas.append(engine.commit(changes, commit_seq=seq))
        return deltas

    def checkpoint(self):
        for engine in self.engines.values():
            engine.checkpoint()
