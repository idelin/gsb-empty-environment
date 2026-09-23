"""Grouped retractable TopK index fed by local change fixtures.

Each change event carries: tx_id, table, pk, before, after, commit_seq.
The index keeps per-group TopK, emits stable-id diffs per committed batch,
and serves fixed-version snapshots with pagination.
"""
import json
import os

from .keys import SortSpec
from .store import BlockStats, Entry, GroupStore

_MISSING = object()


class Snapshot:
    """Immutable view of every group's TopK at a fixed commit version."""

    def __init__(self, version, k, data):
        self.version = version
        self.k = k
        self._data = data

    def groups(self):
        return list(self._data)

    def page(self, group, offset=0, limit=None):
        rows = self._data.get(group, [])
        chunk = rows[offset:] if limit is None else rows[offset:offset + limit]
        return [{"id": r["id"], "values": dict(r["values"])} for r in chunk]


class TopKIndex:
    def __init__(self, spec, k, group_field, table, storage_dir,
                 window_size=None, page_size=64):
        self.spec = spec
        self.k = k
        self.group_field = group_field
        self.table = table
        self.storage_dir = storage_dir
        if window_size is None:
            window_size = max(k, page_size)
        self.window_size = max(k, window_size)
        self.page_size = page_size
        self.stats = BlockStats()
        self.groups = {}     # group value -> GroupStore
        self.id_group = {}   # pk -> group value
        self.commit_seq = 0
        os.makedirs(storage_dir, exist_ok=True)

    # -- group store management ---------------------------------------------
    def _store(self, group):
        st = self.groups.get(group)
        if st is None:
            d = os.path.join(self.storage_dir, "g%05d" % len(self.groups))
            st = GroupStore(self.spec, group, d, self.window_size,
                            self.page_size, self.stats)
            self.groups[group] = st
        return st

    # -- change application ---------------------------------------------------
    def _apply_one(self, ev):
        if ev.get("table") != self.table:
            return
        pk = ev["pk"]
        after = ev.get("after")
        old_group = self.id_group.get(pk, _MISSING)
        if after is None:                       # delete
            if old_group is not _MISSING:
                self.groups[old_group].remove(pk)
                del self.id_group[pk]
        else:
            new_group = after.get(self.group_field)
            if old_group is _MISSING:           # insert
                self._store(new_group).add(Entry(pk, after))
                self.id_group[pk] = new_group
            elif old_group != new_group:        # group migration
                self.groups[old_group].remove(pk)
                self._store(new_group).add(Entry(pk, after))
                self.id_group[pk] = new_group
            else:                               # update in place
                st = self.groups[old_group]
                st.remove(pk)
                st.add(Entry(pk, after))
        if "commit_seq" in ev:
            self.commit_seq = max(self.commit_seq, ev["commit_seq"])

    def apply_batch(self, events):
        """Apply one committed batch; return {group: {entered, exited}}
        with ids in deterministic rank order (stable-id diff)."""
        touched = set()
        for ev in events:
            if ev.get("table") != self.table:
                continue
            g = self.id_group.get(ev["pk"], _MISSING)
            if g is not _MISSING:
                touched.add(g)
            after = ev.get("after")
            if after is not None:
                touched.add(after.get(self.group_field))
        before = {g: [e.id for e in self.groups[g].topk(self.k)]
                  for g in touched if g in self.groups}
        for ev in events:
            self._apply_one(ev)
        diff = {}
        for g in sorted(touched, key=repr):
            after_ids = ([e.id for e in self.groups[g].topk(self.k)]
                         if g in self.groups else [])
            old_set, new_set = set(before.get(g, [])), set(after_ids)
            entered = [i for i in after_ids if i not in old_set]
            exited = [i for i in before.get(g, []) if i not in new_set]
            if entered or exited:
                diff[g] = {"entered": entered, "exited": exited}
        return diff

    # -- reads ------------------------------------------------------------------
    def topk(self, group=None):
        if group is not None:
            st = self.groups.get(group)
            return st.topk(self.k) if st else []
        return {g: st.topk(self.k) for g, st in self.groups.items()}

    def snapshot(self):
        data = {}
        for g, st in self.groups.items():
            data[g] = [{"id": e.id, "values": dict(e.values)}
                       for e in st.topk(self.k)]
        return Snapshot(self.commit_seq, self.k, data)

    # -- persistence -------------------------------------------------------------
    def save(self):
        for st in self.groups.values():
            st.save()
        state = {
            "k": self.k,
            "group_field": self.group_field,
            "table": self.table,
            "window_size": self.window_size,
            "page_size": self.page_size,
            "commit_seq": self.commit_seq,
            "spec": self.spec.to_json(),
            "groups": [{"group": g, "dir": os.path.basename(st.dir)}
                       for g, st in self.groups.items()],
            "id_group": [[pk, g] for pk, g in self.id_group.items()],
        }
        with open(os.path.join(self.storage_dir, "state.json"), "w",
                  encoding="utf-8") as fh:
            json.dump(state, fh)

    @classmethod
    def load(cls, storage_dir):
        with open(os.path.join(storage_dir, "state.json"), "r",
                  encoding="utf-8") as fh:
            state = json.load(fh)
        idx = cls(SortSpec.from_json(state["spec"]), state["k"],
                  state["group_field"], state["table"], storage_dir,
                  window_size=state["window_size"],
                  page_size=state["page_size"])
        idx.commit_seq = state["commit_seq"]
        for rec in state["groups"]:
            st = GroupStore.load(idx.spec,
                                 os.path.join(storage_dir, rec["dir"]),
                                 idx.stats)
            idx.groups[st.group] = st
        idx.id_group = {pk: g for pk, g in state["id_group"]}
        return idx
