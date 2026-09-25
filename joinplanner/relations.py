"""Relations with explicit SQL multiset (bag) and NULL semantics.

No SQL engine is used: joins are executed directly over Python lists of
tuples.  A relation is a *bag* of rows: duplicate rows are significant and
joins multiply multiplicities.  NULL (None) never equals anything, including
itself, so NULL keys never match in an equijoin; unmatched left rows of a
LEFT OUTER join are padded with NULLs.
"""
from __future__ import annotations

import zlib
from collections import Counter
from dataclasses import dataclass

INNER = "INNER"
LEFT_OUTER = "LEFT_OUTER"


@dataclass
class Relation:
    name: str
    columns: tuple[str, ...]
    rows: list[tuple]
    lineage: tuple = ()

    def __post_init__(self):
        self.columns = tuple(self.columns)
        self.rows = [tuple(r) for r in self.rows]
        for row in self.rows:
            if len(row) != len(self.columns):
                raise ValueError(
                    f"{self.name}: row {row!r} does not match columns {self.columns!r}"
                )
        if not self.lineage:
            self.lineage = tuple((self.name, c) for c in self.columns)

    @property
    def nrows(self) -> int:
        return len(self.rows)

    def multiset(self) -> Counter:
        return Counter(self.rows)

    def canonical_multiset(self) -> Counter:
        """Multiset keyed by logical (owning relation, column) -> value.

        Independent of the physical column ordering produced by different
        (equivalent) join orders, so results of reordered plans can be
        compared as multisets of facts.  NULL pads are kept as values.
        """
        return Counter(
            tuple(sorted(zip(self.lineage, row))) for row in self.rows
        )

    def col_index(self, col: str) -> int:
        return self.columns.index(col)


def _output_columns(left: Relation, right: Relation) -> tuple[str, ...]:
    cols = list(left.columns)
    for c in right.columns:
        cols.append(f"{right.name}.{c}" if c in left.columns else c)
    return tuple(cols)


def _hash_join(left: Relation, right: Relation, lkey: str, rkey: str,
               join_type: str) -> Relation:
    li = left.col_index(lkey)
    ri = right.col_index(rkey)
    index: dict = {}
    for rrow in right.rows:
        k = rrow[ri]
        if k is None:
            continue  # NULL never matches
        index.setdefault(k, []).append(rrow)
    pad = (None,) * len(right.columns)
    out: list[tuple] = []
    for lrow in left.rows:
        k = lrow[li]
        matches = index.get(k) if k is not None else None
        if matches:
            for rrow in matches:  # multiset product: duplicates multiply
                out.append(lrow + rrow)
        elif join_type == LEFT_OUTER:
            out.append(lrow + pad)
    return Relation(
        name=f"({left.name}join{right.name})",
        columns=_output_columns(left, right),
        rows=out,
        lineage=left.lineage + tuple((right.name, c) for c in right.columns),
    )


def _stable_bucket(key, partitions: int) -> int:
    # Deterministic across processes (unlike built-in hash for str).
    return zlib.crc32(repr(key).encode("utf-8")) % partitions


def _partitioned_join(left: Relation, right: Relation, lkey: str, rkey: str,
                      join_type: str, partitions: int) -> Relation:
    """Hash-partition both sides, then join per partition.

    Produces exactly the same multiset as the in-memory hash join; used when
    the build side does not fit in the memory budget.
    """
    li = left.col_index(lkey)
    ri = right.col_index(rkey)
    lbuckets: list[list[tuple]] = [[] for _ in range(partitions)]
    rbuckets: list[list[tuple]] = [[] for _ in range(partitions)]
    out: list[tuple] = []
    pad = (None,) * len(right.columns)
    for lrow in left.rows:
        k = lrow[li]
        if k is None:
            if join_type == LEFT_OUTER:
                out.append(lrow + pad)
            continue
        lbuckets[_stable_bucket(k, partitions)].append(lrow)
    for rrow in right.rows:
        k = rrow[ri]
        if k is None:
            continue
        rbuckets[_stable_bucket(k, partitions)].append(rrow)
    for b in range(partitions):
        index: dict = {}
        for rrow in rbuckets[b]:
            index.setdefault(rrow[ri], []).append(rrow)
        for lrow in lbuckets[b]:
            matches = index.get(lrow[li])
            if matches:
                for rrow in matches:
                    out.append(lrow + rrow)
            elif join_type == LEFT_OUTER:
                out.append(lrow + pad)
    return Relation(
        name=f"({left.name}pjoin{right.name})",
        columns=_output_columns(left, right),
        rows=out,
        lineage=left.lineage + tuple((right.name, c) for c in right.columns),
    )


def execute_join(left: Relation, right: Relation, lkey: str, rkey: str,
                 join_type: str = INNER, partitioned: bool = False,
                 partitions: int = 64) -> Relation:
    if join_type not in (INNER, LEFT_OUTER):
        raise ValueError(f"unsupported join type {join_type!r}")
    if partitioned:
        return _partitioned_join(left, right, lkey, rkey, join_type, partitions)
    return _hash_join(left, right, lkey, rkey, join_type)


def exact_ndv(rel: Relation, col: str) -> int:
    idx = rel.col_index(col)
    return len({row[idx] for row in rel.rows if row[idx] is not None})
