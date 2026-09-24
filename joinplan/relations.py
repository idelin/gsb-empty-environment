"""Relations and join execution with explicit NULL and multiset semantics."""
from __future__ import annotations

from collections import Counter
from dataclasses import dataclass
from zlib import crc32

INNER = "inner"
LEFT = "left"


@dataclass
class Relation:
    """A base relation stored as a multiset of rows (duplicates allowed)."""

    name: str
    columns: tuple[str, ...]
    rows: list[tuple]

    def __post_init__(self):
        self.columns = tuple(self.columns)
        for row in self.rows:
            if len(row) != len(self.columns):
                raise ValueError(f"{self.name}: row {row!r} does not match columns")

    @property
    def cardinality(self) -> int:
        return len(self.rows)

    def col_index(self, col: str) -> int:
        return self.columns.index(col)


def _stable_hash(key) -> int:
    """Deterministic hash (independent of PYTHONHASHSEED) for replayability."""
    return crc32(repr(key).encode("utf-8"))


def hash_join(left_rows, left_idx, right_rows, right_idx, right_arity, kind=INNER):
    """Equi-join two row multisets.

    NULL semantics: None keys never match, not even each other.
    Multiset semantics: each matching pair produces one output row, so
    duplicate keys multiply. LEFT preserves unmatched left rows, padded
    with None on the right side.
    """
    index = {}
    for r in right_rows:
        key = r[right_idx]
        if key is None:
            continue
        index.setdefault(key, []).append(r)
    out = []
    pad = (None,) * right_arity
    for l in left_rows:
        key = l[left_idx]
        matches = index.get(key) if key is not None else None
        if matches:
            out.extend(l + m for m in matches)
        elif kind == LEFT:
            out.append(l + pad)
    return out


def partitioned_join(left_rows, left_idx, right_rows, right_idx, right_arity,
                     kind, num_partitions, row_width):
    """Grace-hash style join used when inputs exceed the memory budget.

    Both sides are hash-partitioned (simulating spill to disk) and joined
    partition by partition. Returns (rows, spilled_bytes) where spilled
    bytes accounts for writing and re-reading both inputs once.
    """
    num_partitions = max(2, int(num_partitions))
    parts_l = [[] for _ in range(num_partitions)]
    parts_r = [[] for _ in range(num_partitions)]
    null_left = []
    for row in left_rows:
        key = row[left_idx]
        if key is None:
            if kind == LEFT:
                null_left.append(row)
            continue
        parts_l[_stable_hash(key) % num_partitions].append(row)
    for row in right_rows:
        key = row[right_idx]
        if key is None:
            continue
        parts_r[_stable_hash(key) % num_partitions].append(row)
    out = []
    for p in range(num_partitions):
        out.extend(hash_join(parts_l[p], left_idx, parts_r[p], right_idx,
                             right_arity, kind))
    if kind == LEFT:
        pad = (None,) * right_arity
        out.extend(l + pad for l in null_left)
    spilled = 2 * (len(left_rows) + len(right_rows)) * row_width
    return out, spilled


def named_multiset(columns, rows):
    """Order-independent multiset of rows keyed by column name.

    Two executions that join the same relations in different orders produce
    tuples with different column layouts; this normalizes them so the
    multisets can be compared for equality.
    """
    return Counter(tuple(sorted(zip(columns, row))) for row in rows)
