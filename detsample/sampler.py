"""Deterministic group-quota sampler.

Core rule
---------
Every row gets a stable random key:  key = H(seed, row_id), where H is
BLAKE2b-128.  The key depends only on (seed, row_id) -- never on input
order, batch splitting, or other rows.  Per group, the sample is the
`quota` visible rows with the smallest keys (ties broken by the encoded
row id, giving a total deterministic order).

Consequences, by construction:
- batch splitting and input order do not affect the sample;
- merging shards = taking the k smallest keys of the union, which is
  exactly the full-data result (we never concatenate shard samples and
  then uniformly truncate);
- retracting a sampled row automatically replenishes the sample from
  the remaining legal candidates, because all candidates are retained
  in key order;
- quota 0 yields an empty sample; quota > group size yields the whole
  group plus a reported shortfall.

Versioned retraction
--------------------
Rows carry a version.  retract(row_id, v) hides every version of that
row with version <= v.  A later row version > v is visible again.
Tombstones are monotone (only the max version is kept), so retraction
is order-independent and merges cleanly across shards and checkpoints.
A tombstone is kept even when its row has not (yet) been seen, so a
retraction that arrives before its row still takes effect after merge.
"""

from __future__ import annotations

import hashlib
import pickle
from typing import Any, Callable, Iterable, Mapping, NamedTuple, Optional

from .table import Batch
from .values import encode_value

KeyFn = Callable[[Any, Any], int]


def default_key_fn(seed: Any, row_id: Any) -> int:
    """Stable 128-bit key: depends only on (seed, row_id)."""
    h = hashlib.blake2b(digest_size=16, person=b"detsample.v1")
    h.update(encode_value(seed))
    h.update(encode_value(row_id))
    return int.from_bytes(h.digest(), "big")


class SampleResult(NamedTuple):
    group: Any
    quota: int
    rows: tuple          # sampled row payloads, in deterministic key order
    row_ids: tuple       # their stable row ids
    actual: int          # actual sample size (<= quota)
    shortfall: int       # max(0, quota - available candidates)


class _Entry(NamedTuple):
    row_id: Any
    version: int
    group: Any
    values: tuple


class Sampler:
    def __init__(
        self,
        seed: Any,
        quotas: Optional[Mapping[Any, int]] = None,
        key_fn: Optional[KeyFn] = None,
    ):
        self.seed = seed
        self._key_fn: KeyFn = key_fn if key_fn is not None else default_key_fn
        self._entries: dict[bytes, _Entry] = {}        # idkey -> latest row version
        self._tomb: dict[bytes, tuple[int, Any]] = {}  # idkey -> (max retracted ver, row_id)
        self._quotas: dict[bytes, int] = {}            # gkey  -> quota
        self._group_values: dict[bytes, Any] = {}      # gkey  -> original group value
        self._key_cache: dict[bytes, int] = {}         # idkey -> random key
        if quotas:
            for group, quota in quotas.items():
                self.set_quota(group, quota)

    # ---------------- ingestion ----------------

    def set_quota(self, group: Any, quota: int) -> None:
        if not isinstance(quota, int) or isinstance(quota, bool) or quota < 0:
            raise ValueError(f"quota must be a non-negative int, got {quota!r}")
        gkey = encode_value(group)
        self._quotas[gkey] = quota
        self._group_values.setdefault(gkey, group)

    def add_row(self, row_id: Any, group: Any, values: Optional[tuple] = None,
                version: int = 0) -> None:
        if not isinstance(version, int) or isinstance(version, bool) or version < 0:
            raise ValueError(f"version must be a non-negative int, got {version!r}")
        idkey = encode_value(row_id)
        gkey = encode_value(group)
        existing = self._entries.get(idkey)
        if existing is not None and existing.version >= version:
            return  # keep the newest version; equal version -> first wins
        if values is None:
            values = (row_id,)
        self._entries[idkey] = _Entry(row_id, version, group, tuple(values))
        self._group_values.setdefault(gkey, group)

    def add_batch(self, batch: Batch, id_col: str, group_col: str,
                  version_col: Optional[str] = None) -> None:
        idi = batch.column_index(id_col)
        gi = batch.column_index(group_col)
        vi = batch.column_index(version_col) if version_col is not None else None
        for row in batch.rows:
            version = row[vi] if vi is not None else 0
            self.add_row(row[idi], row[gi], values=row, version=version)

    def retract(self, row_id: Any, version: int) -> None:
        """Hide all versions of `row_id` with version <= `version`."""
        idkey = encode_value(row_id)
        current = self._tomb.get(idkey)
        if current is None or version > current[0]:
            self._tomb[idkey] = (version, row_id)

    # ---------------- sampling ----------------

    def _key(self, idkey: bytes, row_id: Any) -> int:
        key = self._key_cache.get(idkey)
        if key is None:
            key = self._key_fn(self.seed, row_id)
            self._key_cache[idkey] = key
        return key

    def _visible_by_group(self) -> dict[bytes, list[tuple[tuple, _Entry]]]:
        groups: dict[bytes, list[tuple[tuple, _Entry]]] = {}
        for idkey, entry in self._entries.items():
            tomb = self._tomb.get(idkey)
            if tomb is not None and entry.version <= tomb[0]:
                continue  # retracted
            gkey = encode_value(entry.group)
            sort_key = (self._key(idkey, entry.row_id), idkey)
            groups.setdefault(gkey, []).append((sort_key, entry))
        return groups

    def sample(self) -> list[SampleResult]:
        """Deterministic sample for every known group, sorted by group key."""
        groups = self._visible_by_group()
        results: list[SampleResult] = []
        for gkey in sorted(set(self._quotas) | set(groups)):
            quota = self._quotas.get(gkey, 0)
            candidates = sorted(groups.get(gkey, []), key=lambda t: t[0])
            chosen = candidates[:quota]
            results.append(SampleResult(
                group=self._group_values[gkey],
                quota=quota,
                rows=tuple(e.values for _, e in chosen),
                row_ids=tuple(e.row_id for _, e in chosen),
                actual=len(chosen),
                shortfall=max(0, quota - len(candidates)),
            ))
        return results

    # ---------------- shard merge ----------------

    def export_shard(self) -> dict:
        """Serializable shard state: ALL candidate rows + tombstones.

        Keeping every candidate (not just the current top-k) is what makes
        later retraction replenishable and merging exactly equivalent to
        processing the full visible data in one pass.
        """
        return {
            "seed": self.seed,
            "entries": sorted(
                ((e.row_id, e.version, e.group, e.values)
                 for e in self._entries.values()),
                key=lambda t: encode_value(t[0]),
            ),
            "tombstones": sorted(
                ((row_id, version) for version, row_id in self._tomb.values()),
                key=lambda t: encode_value(t[0]),
            ),
        }

    @classmethod
    def from_shards(cls, seed: Any, quotas: Mapping[Any, int],
                    shards: Iterable[dict],
                    key_fn: Optional[KeyFn] = None) -> "Sampler":
        """Merge shard exports: union of rows (max version wins) and union of
        tombstones (max version wins).  Sampling then takes the k smallest
        keys of the union -- identical to one pass over the full data."""
        merged = cls(seed, quotas, key_fn)
        for shard in shards:
            if shard["seed"] != seed:
                raise ValueError("shard seed mismatch")
            for row_id, version, group, values in shard["entries"]:
                merged.add_row(row_id, group, values, version)
            for row_id, version in shard["tombstones"]:
                merged.retract(row_id, version)
        return merged

    # ---------------- checkpoint ----------------

    def checkpoint(self) -> bytes:
        state = {
            "seed": self.seed,
            "entries": self.export_shard()["entries"],
            "tombstones": self.export_shard()["tombstones"],
            "quotas": sorted(
                ((self._group_values[g], q) for g, q in self._quotas.items()),
                key=lambda t: encode_value(t[0]),
            ),
        }
        return pickle.dumps(state, protocol=4)

    @classmethod
    def restore(cls, data: bytes, key_fn: Optional[KeyFn] = None) -> "Sampler":
        state = pickle.loads(data)
        sampler = cls(state["seed"], key_fn=key_fn)
        for group, quota in state["quotas"]:
            sampler.set_quota(group, quota)
        for row_id, version, group, values in state["entries"]:
            sampler.add_row(row_id, group, values, version)
        for row_id, version in state["tombstones"]:
            sampler.retract(row_id, version)
        return sampler
