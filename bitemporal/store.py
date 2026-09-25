"""The bitemporal store: atomic transactions, versioned queries, provenance."""

from __future__ import annotations

from dataclasses import replace

from .decompose import decompose
from .index import KeyIndex, QueryStats
from .model import (REPRIORITIZE, RETRACT, UPSERT, Competitor, Explanation,
                    Fragment, LiveRecord, Record, from_key, to_key)
from .timeline import Instant, Timeline


class BitemporalStore:
    def __init__(self, timeline: Timeline | None = None) -> None:
        self.timeline = timeline or Timeline()
        self._version = 0
        self._records: list[Record] = []            # append-only history log
        self._by_id: dict[int, Record] = {}
        self._live: dict[str, dict[int, LiveRecord]] = {}
        self._retracted_at: dict[int, int] = {}
        self._priority_history: dict[int, list[tuple[int, int]]] = {}
        self._index: dict[str, KeyIndex] = {}
        self._ids_by_key: dict[str, list[int]] = {}
        self._next_id = 1

    # -- inspection ---------------------------------------------------------
    @property
    def version(self) -> int:
        return self._version

    @property
    def total_records(self) -> int:
        return len(self._records)

    def keys(self) -> list[str]:
        return sorted(self._index)

    def record(self, record_id: int) -> Record:
        return self._by_id[record_id]

    def record_ids(self, key: str) -> list[int]:
        return list(self._ids_by_key.get(key, ()))

    def fragments(self, key: str) -> list[Fragment]:
        idx = self._index.get(key)
        return list(idx.fragments) if idx else []

    def retracted_at(self, record_id: int) -> int | None:
        return self._retracted_at.get(record_id)

    def priority_at(self, record_id: int, version: int) -> int:
        prio = 0
        for ver, p in self._priority_history[record_id]:
            if ver > version:
                break
            prio = p
        return prio

    # -- transactions ---------------------------------------------------------
    def transaction(self) -> "_Transaction":
        return _Transaction(self)

    # -- queries (never touch the history log) --------------------------------
    def _resolve_version(self, as_of: int | None) -> int:
        version = self._version if as_of is None else as_of
        if not 0 <= version <= self._version:
            raise ValueError(f"observation version {version} outside [0, {self._version}]")
        return version

    def fragment_at(self, key: str, when: Instant, as_of: int | None = None,
                    stats: QueryStats | None = None) -> Fragment | None:
        version = self._resolve_version(as_of)
        idx = self._index.get(key)
        if idx is None:
            return None
        hit = idx.query(version, version + 1, when, Instant(when.ticks + 1), stats)
        return hit[0] if hit else None

    def value_at(self, key: str, when: Instant, as_of: int | None = None,
                 stats: QueryStats | None = None):
        """The business value visible at ``when`` as of observation ``as_of``."""
        frag = self.fragment_at(key, when, as_of, stats)
        return frag.value if frag is not None else None

    def range_query(self, key: str, valid_from: Instant | None = None,
                    valid_to: Instant | None = None, ver_from: int = 0,
                    ver_to: int | None = None,
                    stats: QueryStats | None = None) -> list[Fragment]:
        """All visible rectangles intersecting valid x version query box."""
        idx = self._index.get(key)
        if idx is None:
            return []
        v_hi = self._version + 1 if ver_to is None else ver_to
        return idx.query(ver_from, v_hi, valid_from, valid_to, stats)

    def explain(self, key: str, when: Instant, as_of: int | None = None) -> Explanation:
        """Provenance: the visible record plus every suppressed competitor."""
        version = self._resolve_version(as_of)
        frag = self.fragment_at(key, when, version)
        live_at = []
        for rid in self._ids_by_key.get(key, ()):  # per-key only, never the full log
            rec = self._by_id[rid]
            if rec.version > version:
                continue
            retracted = self._retracted_at.get(rid)
            if retracted is not None and retracted <= version:
                continue
            if from_key(rec.valid_from) <= from_key(when) < to_key(rec.valid_to):
                live_at.append((rec, self.priority_at(rid, version)))
        live_at.sort(key=lambda rp: (-rp[1], rp[0].record_id))
        views = []
        for i, (rec, prio) in enumerate(live_at):
            if i == 0:
                status = "visible"
            elif prio < live_at[0][1]:
                status = f"suppressed: lower priority than record #{live_at[0][0].record_id}"
            else:
                status = (f"suppressed: same priority as record "
                          f"#{live_at[0][0].record_id}, which was recorded earlier")
            views.append(Competitor(rec.record_id, prio, rec.version,
                                    rec.valid_from, rec.valid_to, rec.value, status))
        return Explanation(key, when, version, frag.value if frag else None,
                           frag, tuple(views))


class _Transaction:
    """Groups upserts, retractions and priority changes into one atomic version.

    Phase 1 validates every operation and mutates only private working
    copies; phase 2 computes the canonical fragment diff; phase 3 applies
    the precomputed changes.  Any failure in phase 1 leaves the store
    completely untouched, so a transaction is all-or-nothing.
    """

    def __init__(self, store: BitemporalStore) -> None:
        self._store = store
        self._ops: list[tuple] = []
        self._committed = False
        self.entry_ids: list[int] = []

    def upsert(self, key, valid_from, valid_to=None, value=None, priority=0) -> int:
        self._ops.append((UPSERT, key, valid_from, valid_to, value, priority))
        return len(self._ops) - 1

    def retract(self, record_id: int) -> int:
        self._ops.append((RETRACT, record_id))
        return len(self._ops) - 1

    def change_priority(self, record_id: int, priority: int) -> int:
        self._ops.append((REPRIORITIZE, record_id, priority))
        return len(self._ops) - 1

    def __enter__(self) -> "_Transaction":
        return self

    def __exit__(self, exc_type, exc, tb) -> bool:
        if exc_type is None and not self._committed:
            self.commit()
        return False

    def commit(self) -> list[int]:
        if self._committed:
            raise RuntimeError("transaction already committed")
        self._committed = True
        store = self._store
        if not self._ops:
            return []
        version = store._version + 1
        base_id = store._next_id

        # ---- phase 1: validate; mutate only working copies -----------------
        live_work: dict[str, dict[int, LiveRecord]] = {}

        def work(key: str) -> dict[int, LiveRecord]:
            if key not in live_work:
                live_work[key] = dict(store._live.get(key, {}))
            return live_work[key]

        records: list[Record] = []
        retractions: dict[int, int] = {}
        prio_changes: dict[int, int] = {}
        for i, op in enumerate(self._ops):
            rid = base_id + i
            kind = op[0]
            if kind == UPSERT:
                _, key, vf, vt, value, prio = op
                if vf is not None and vt is not None and not vf.ticks < vt.ticks:
                    raise ValueError("valid_from must be strictly before valid_to")
                work(key)[rid] = LiveRecord(rid, vf, vt, value, prio)
                records.append(Record(rid, version, UPSERT, key, vf, vt, value, prio))
            else:
                target = op[1]
                rec = store._by_id.get(target)
                if rec is None or rec.op != UPSERT:
                    raise KeyError(f"no committed upsert record #{target}")
                if target in store._retracted_at or target in retractions:
                    raise KeyError(f"record #{target} is already retracted")
                key = rec.key
                if target not in work(key):
                    raise KeyError(f"record #{target} is not live")
                if kind == RETRACT:
                    del work(key)[target]
                    retractions[target] = rid
                    records.append(Record(rid, version, RETRACT, key,
                                          None, None, target=target))
                else:
                    new_prio = op[2]
                    work(key)[target] = replace(work(key)[target], priority=new_prio)
                    prio_changes[target] = new_prio
                    records.append(Record(rid, version, REPRIORITIZE, key,
                                          None, None, priority=new_prio, target=target))

        # ---- phase 2: canonical decomposition + rectangle diff -------------
        index_updates = []
        for key, live_map in live_work.items():
            pieces = decompose(list(live_map.values()))
            idx = store._index.setdefault(key, KeyIndex())
            frontier_ids = {f.identity() for f in idx.frontier()}
            piece_ids = set()
            adds = []
            for vf, vt, rec in pieces:
                ident = (vf, vt, rec.value, rec.priority, rec.record_id)
                piece_ids.add(ident)
                if ident not in frontier_ids:
                    adds.append(Fragment(key, vf, vt, version, None,
                                         rec.value, rec.priority, rec.record_id))
            closes = [f for f in idx.frontier() if f.identity() not in piece_ids]
            index_updates.append((key, closes, adds))

        # ---- phase 3: apply the precomputed changes ------------------------
        for key, live_map in live_work.items():
            if live_map:
                store._live[key] = live_map
            else:
                store._live.pop(key, None)
        for key, closes, adds in index_updates:
            store._index[key].apply(closes, adds, version)
        for rec in records:
            store._records.append(rec)
            store._by_id[rec.record_id] = rec
            if rec.op == UPSERT:
                store._ids_by_key.setdefault(rec.key, []).append(rec.record_id)
                store._priority_history[rec.record_id] = [(version, rec.priority)]
        for target in retractions:
            store._retracted_at[target] = version
        for target, prio in prio_changes.items():
            store._priority_history[target].append((version, prio))
        store._next_id = base_id + len(self._ops)
        store._version = version
        self.entry_ids = [rec.record_id for rec in records]
        return self.entry_ids
