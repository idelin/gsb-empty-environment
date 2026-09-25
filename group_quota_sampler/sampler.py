"""Deterministic group-quota sampling with stable row ids and an explicit seed.

Core rule: every visible row gets a deterministic random key
``key = H(seed, row_id)`` (128-bit, blake2b).  Inside each group the sample
is the ``quota`` rows with the smallest keys.  This makes the result

* independent of input order and of how rows are split into batches,
* mergeable across shards (the global top-k per group is contained in the
  union of each shard's top-k, so shards export their per-group top-k
  candidates and the merger re-ranks the union -- never "concatenate shard
  samples and truncate uniformly"),
* repairable under retractions (a deleted sampled row is replaced by the
  next best legal candidate), and
* reproducible from a checkpoint (the persisted state is the full visible
  key index, so recovery equals an independent full recomputation).

Versioning: inserts carry a version; a retraction at version ``v`` hides all
inserts of that row id with version ``<= v``.  A later insert with a higher
version makes the row visible again.
"""

from __future__ import annotations

from dataclasses import dataclass

from .encoding import (
    canonical_group,
    canonical_value,
    encode_value,
    normalize_seed,
    row_key,
    value_from_json,
    value_to_json,
)
from .table import Column

CHECKPOINT_FORMAT = 1


def _canon_row(row):
    return tuple(canonical_value(v) for v in row)


@dataclass(eq=False)
class GroupResult:
    """Sampling outcome for one group."""

    group: tuple          # raw group-by values
    quota: int            # requested quota (0 and > group size are both legal)
    actual: int           # rows actually sampled
    deficit: int          # max(0, quota - actual)
    row_ids: tuple        # sampled stable row ids, ordered by random key
    rows: tuple           # sampled full rows, same order

    def __eq__(self, other):
        if not isinstance(other, GroupResult):
            return NotImplemented
        return (
            _canon_row(self.group) == _canon_row(other.group)
            and self.quota == other.quota
            and self.actual == other.actual
            and self.deficit == other.deficit
            and _canon_row(self.row_ids) == _canon_row(other.row_ids)
            and tuple(_canon_row(r) for r in self.rows)
            == tuple(_canon_row(r) for r in other.rows)
        )

    __hash__ = None


@dataclass(eq=False)
class SampleResult:
    """Full sampling outcome; groups sorted by canonical group key."""

    groups: tuple

    def __eq__(self, other):
        if not isinstance(other, SampleResult):
            return NotImplemented
        return self.groups == other.groups

    __hash__ = None

    def by_group(self):
        return {_canon_row(g.group): g for g in self.groups}


class _Entry:
    __slots__ = ("row_id", "group_raw", "group_canon", "key", "version", "row")

    def __init__(self, row_id, group_raw, key, version, row):
        self.row_id = row_id
        self.group_raw = group_raw
        self.group_canon = canonical_group(group_raw)
        self.key = key
        self.version = version
        self.row = row


class GroupQuotaSampler:
    """Incremental deterministic sampler over typed batches."""

    def __init__(self, *, seed, columns, id_column, group_columns,
                 quotas=None, default_quota=0):
        self._seed_bytes = normalize_seed(seed)
        self.columns = tuple(columns)
        self._id_idx = self._column_index(id_column)
        group_columns = tuple(group_columns)
        if not group_columns:
            raise ValueError("need at least one group column")
        self._group_idx = tuple(self._column_index(c) for c in group_columns)
        self.group_columns = group_columns
        self.id_column = id_column
        if default_quota < 0:
            raise ValueError("default_quota must be >= 0")
        self.default_quota = default_quota
        self._quotas = {}       # canonical group key -> quota
        self._quota_raw = {}    # canonical group key -> raw group tuple
        for group, q in (quotas or {}).items():
            self.set_quota(group, q)
        # row identity is the deterministic encoding of the stable row id;
        # it is hashable, totally ordered and JSON-friendly (as hex)
        self._entries = {}      # encode_value(row_id) -> _Entry (latest insert)
        self._tombstones = {}   # encode_value(row_id) -> retraction version

    # -- schema --------------------------------------------------------------

    def _column_index(self, name):
        for i, c in enumerate(self.columns):
            if c.name == name:
                return i
        raise KeyError(f"unknown column: {name!r}")

    # -- quotas --------------------------------------------------------------

    def set_quota(self, group, quota):
        """Set the quota for one group (raw value tuple). 0 is legal."""
        if quota < 0:
            raise ValueError("quota must be >= 0")
        group = tuple(group)
        canon = canonical_group(group)
        self._quotas[canon] = quota
        self._quota_raw[canon] = group

    def effective_quota(self, group_canon):
        return self._quotas.get(group_canon, self.default_quota)

    # -- ingestion -----------------------------------------------------------

    def add_batch(self, batch, version=0):
        """Ingest one typed batch.  Batch boundaries never affect the sample."""
        if tuple(batch.columns) != self.columns:
            raise ValueError("batch schema does not match sampler schema")
        if version < 0:
            raise ValueError("version must be >= 0")
        for row in batch.rows:
            row_id = row[self._id_idx]
            rid = encode_value(row_id)
            existing = self._entries.get(rid)
            if existing is not None and existing.version >= version:
                continue  # stale re-insert, keep the newer version
            group_raw = tuple(row[i] for i in self._group_idx)
            self._entries[rid] = _Entry(
                row_id=row_id,
                group_raw=group_raw,
                key=row_key(self._seed_bytes, row_id),
                version=version,
                row=row,
            )

    def retract(self, row_ids, version):
        """Versioned retraction: hides inserts of these ids with version <= v."""
        if version < 0:
            raise ValueError("version must be >= 0")
        for row_id in row_ids:
            rid = encode_value(row_id)
            if version > self._tombstones.get(rid, -1):
                self._tombstones[rid] = version

    # -- sampling --------------------------------------------------------------

    def _visible_entries(self):
        for rid, entry in self._entries.items():
            if entry.version > self._tombstones.get(rid, -1):
                yield entry

    @staticmethod
    def _rank(entry):
        # deterministic order: random key, tie-broken by encoded row id
        return (entry.key, encode_value(entry.row_id))

    def _grouped_visible(self):
        by_group = {}
        for entry in self._visible_entries():
            by_group.setdefault(entry.group_canon, []).append(entry)
        return by_group

    def sample(self):
        by_group = self._grouped_visible()
        results = []
        for canon in set(by_group) | set(self._quotas):
            entries = by_group.get(canon, [])
            quota = self.effective_quota(canon)
            ranked = sorted(entries, key=self._rank)
            chosen = ranked[:quota]
            raw = self._quota_raw.get(canon)
            if raw is None:
                raw = entries[0].group_raw
            results.append(GroupResult(
                group=tuple(raw),
                quota=quota,
                actual=len(chosen),
                deficit=max(0, quota - len(chosen)),
                row_ids=tuple(e.row_id for e in chosen),
                rows=tuple(e.row for e in chosen),
            ))
        results.sort(key=lambda g: canonical_group(g.group))
        return SampleResult(groups=tuple(results))

    # -- shard export / merge ----------------------------------------------------

    def export_shard(self):
        """Export per-group top-quota candidates (with keys) for merging.

        This is *not* a final sample to be concatenated; it carries enough
        ranked candidates per group for the merger to reconstruct exactly
        the sample that a single full-data pass would produce.
        """
        by_group = self._grouped_visible()
        groups = []
        quotas = {}
        for canon, entries in by_group.items():
            quota = self.effective_quota(canon)
            ranked = sorted(entries, key=self._rank)
            kept = ranked[:quota]
            quotas[canon] = (entries[0].group_raw, quota)
            groups.append({
                "group": [value_to_json(v) for v in entries[0].group_raw],
                "candidates": [
                    {
                        "row_id": value_to_json(e.row_id),
                        "key": e.key,
                        "row": [value_to_json(v) for v in e.row],
                    }
                    for e in kept
                ],
            })
        for canon, q in self._quotas.items():
            quotas.setdefault(canon, (self._quota_raw[canon], q))
        return {
            "format": CHECKPOINT_FORMAT,
            "default_quota": self.default_quota,
            "quotas": [
                [[value_to_json(v) for v in raw], q]
                for canon, (raw, q) in sorted(quotas.items())
            ],
            "groups": groups,
        }

    @staticmethod
    def merge_shards(exports):
        """Merge shard exports into the exact full-data sample.

        Every shard must have been produced with the same seed, quotas and
        stable-id rule; each shard contributes its per-group top-quota
        candidates and the merger re-ranks the union per group.
        """
        if not exports:
            raise ValueError("nothing to merge")
        default_quota = exports[0]["default_quota"]
        quota_map = {}   # canonical group -> (raw group tuple, quota)
        candidates = {}  # canonical group -> {row-id-encoding: (key, row_id, row)}
        for ex in exports:
            if ex["default_quota"] != default_quota:
                raise ValueError("shards disagree on default_quota")
            for group_json, q in ex["quotas"]:
                raw = tuple(value_from_json(v) for v in group_json)
                canon = canonical_group(raw)
                if canon in quota_map and quota_map[canon][1] != q:
                    raise ValueError("shards disagree on quotas")
                quota_map[canon] = (raw, q)
            for g in ex["groups"]:
                raw = tuple(value_from_json(v) for v in g["group"])
                canon = canonical_group(raw)
                quota_map.setdefault(canon, (raw, default_quota))
                pool = candidates.setdefault(canon, {})
                for cand in g["candidates"]:
                    row_id = value_from_json(cand["row_id"])
                    # the same stable id may appear in several shards; its
                    # key is identical, so keeping any copy is correct
                    pool[encode_value(row_id)] = (
                        cand["key"],
                        row_id,
                        tuple(value_from_json(v) for v in cand["row"]),
                    )
        results = []
        for canon in set(quota_map) | set(candidates):
            raw, quota = quota_map[canon]
            pool = candidates.get(canon, {})
            ranked = sorted(pool.values(), key=lambda t: (t[0], encode_value(t[1])))
            chosen = ranked[:quota]
            results.append(GroupResult(
                group=raw,
                quota=quota,
                actual=len(chosen),
                deficit=max(0, quota - len(chosen)),
                row_ids=tuple(t[1] for t in chosen),
                rows=tuple(t[2] for t in chosen),
            ))
        results.sort(key=lambda g: canonical_group(g.group))
        return SampleResult(groups=tuple(results))

    # -- checkpointing -----------------------------------------------------------

    def checkpoint(self):
        """Serialisable (JSON-safe) snapshot of the full visible key index."""
        return {
            "format": CHECKPOINT_FORMAT,
            "seed": self._seed_bytes.hex(),
            "columns": [[c.name, c.type] for c in self.columns],
            "id_column": self.id_column,
            "group_columns": list(self.group_columns),
            "default_quota": self.default_quota,
            "quotas": [
                [[value_to_json(v) for v in self._quota_raw[canon]], q]
                for canon, q in sorted(self._quotas.items())
            ],
            "entries": [
                {
                    "rid": rid.hex(),
                    "row_id": value_to_json(e.row_id),
                    "key": e.key,
                    "version": e.version,
                    "row": [value_to_json(v) for v in e.row],
                }
                for rid, e in sorted(self._entries.items())
            ],
            "tombstones": [
                [rid.hex(), version]
                for rid, version in sorted(self._tombstones.items())
            ],
        }

    @classmethod
    def restore(cls, state):
        """Rebuild a sampler from ``checkpoint()``; continuing afterwards
        yields exactly the independent full-reference result."""
        if state["format"] != CHECKPOINT_FORMAT:
            raise ValueError("unsupported checkpoint format")
        columns = [Column(name, typ) for name, typ in state["columns"]]
        sampler = cls(
            seed=bytes.fromhex(state["seed"]),
            columns=columns,
            id_column=state["id_column"],
            group_columns=state["group_columns"],
            default_quota=state["default_quota"],
        )
        for group_json, q in state["quotas"]:
            sampler.set_quota(tuple(value_from_json(v) for v in group_json), q)
        for e in state["entries"]:
            row = tuple(value_from_json(v) for v in e["row"])
            group_raw = tuple(row[i] for i in sampler._group_idx)
            sampler._entries[bytes.fromhex(e["rid"])] = _Entry(
                row_id=value_from_json(e["row_id"]),
                group_raw=group_raw,
                key=e["key"],
                version=e["version"],
                row=row,
            )
        for rid_hex, version in state["tombstones"]:
            sampler._tombstones[bytes.fromhex(rid_hex)] = version
        return sampler
