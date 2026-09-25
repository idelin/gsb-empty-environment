"""Equi-join (inner / left / full outer) with hash partitioning and spill.

Algorithm: grace hash join.

1. Both inputs are streamed batch-by-batch and partitioned by a stable
   hash of the join key. Rows whose key contains NULL or NaN are routed
   to a dedicated "non-matchable" partition: they can never equi-match.
2. Each matching partition pair is joined independently. If the right
   (build) partition fits the row budget, build a hash map and stream
   the left side. Otherwise the pair is recursively re-partitioned.
3. Re-partitioning that makes no progress (e.g. every key identical, so
   all rows land in one child again) falls back to a block-nested-loop
   join over that partition -- recursion is therefore always bounded.

Duplicate keys expand with Cartesian multiplicity (2 left x 3 right = 6
output rows). Output order is definable via the ``order`` parameter.
"""
from __future__ import annotations

import hashlib
import itertools
import math
import os
import pickle
import tempfile

from .batch import RecordBatch
from .spill import SpillPartition
from .types import NULL, ColType

HOWS = ("inner", "left", "full")
ORDERS = ("left", "partition")


def _key_matchable(key) -> bool:
    for v in key:
        if v is NULL:
            return False
        if isinstance(v, float) and math.isnan(v):
            return False
    return True


def _stable_hash(key) -> int:
    """Process-independent hash so partition layout is deterministic."""
    digest = hashlib.blake2b(pickle.dumps(key, protocol=4), digest_size=8)
    return int.from_bytes(digest.digest(), "little")


class JoinResult:
    """Materialized join output: a schema plus row tuples."""

    def __init__(self, schema, rows, stats):
        self.schema = tuple(schema)
        self._rows = rows
        self.stats = stats

    def __iter__(self):
        return iter(self._rows)

    def __len__(self):
        return len(self._rows)

    def rows(self):
        return list(self._rows)

    def to_batches(self, batch_size: int = 1024):
        return [
            RecordBatch(self.schema, self._rows[i:i + batch_size])
            for i in range(0, len(self._rows), batch_size)
        ]


class _Ctx:
    def __init__(self, how, nparts, mem, order, max_depth, spill_dir,
                 lkey_idx, rkey_idx, n_left_cols, n_right_cols):
        self.how = how
        self.nparts = nparts
        self.mem = max(1, mem)
        self.order = order
        self.max_depth = max_depth
        self.spill_dir = spill_dir
        self.lkey_idx = lkey_idx
        self.rkey_idx = rkey_idx
        self.n_left_cols = n_left_cols
        self.n_right_cols = n_right_cols
        self.matched_right = set()  # right seq ids that matched (full join)
        self.out = []               # (sort_key, row)
        self.partitions = []        # every SpillPartition, for cleanup
        self._file_seq = 0
        self.stats = {
            "spilled_partitions": 0,
            "repartition_rounds": 0,
            "block_nested_loop_partitions": 0,
        }

    def new_partition(self, tag: str) -> SpillPartition:
        self._file_seq += 1
        path = os.path.join(self.spill_dir, f"part_{self._file_seq}_{tag}.spill")
        part = SpillPartition(path, self.mem // self.nparts or 1)
        self.partitions.append(part)
        return part

    # -- output emission -------------------------------------------------
    def emit_match(self, lseq, lrow, rseq, rrow):
        self.matched_right.add(rseq)
        self.out.append(((0, lseq, rseq), lrow + rrow))

    def emit_left_unmatched(self, lseq, lrow):
        self.out.append(((0, lseq, -1), lrow + (NULL,) * self.n_right_cols))

    def emit_right_unmatched(self, rseq, rrow):
        self.out.append(((1, rseq, 0), (NULL,) * self.n_left_cols + rrow))

    @property
    def keep_left(self):
        return self.how in ("left", "full")


def _lkey(ctx, row):
    return tuple(row[i] for i in ctx.lkey_idx)


def _rkey(ctx, row):
    return tuple(row[i] for i in ctx.rkey_idx)


def _hash_probe(ctx, lpart, rpart):
    """Build hash on the (in-memory) right partition, stream the left."""
    table = {}
    for rseq, rrow in rpart.rows():
        table.setdefault(_rkey(ctx, rrow), []).append((rseq, rrow))
    for lseq, lrow in lpart.rows():
        matches = table.get(_lkey(ctx, lrow))
        if matches:
            for rseq, rrow in matches:
                ctx.emit_match(lseq, lrow, rseq, rrow)
        elif ctx.keep_left:
            ctx.emit_left_unmatched(lseq, lrow)


def _block_nested_loop(ctx, lpart, rpart):
    """O(mem) fallback when partitioning cannot split a partition.

    Left side is read in chunks of at most ``mem`` rows; the right
    partition is streamed from disk once per chunk. Always terminates,
    even when every join key is identical.
    """
    ctx.stats["block_nested_loop_partitions"] += 1
    left_iter = lpart.rows()
    while True:
        chunk = list(itertools.islice(left_iter, ctx.mem))
        if not chunk:
            return
        hit = [False] * len(chunk)
        lkeys = [_lkey(ctx, row) for _, row in chunk]
        for rseq, rrow in rpart.rows():
            rkey = _rkey(ctx, rrow)
            for i, lkey in enumerate(lkeys):
                if lkey == rkey:
                    hit[i] = True
                    lseq, lrow = chunk[i]
                    ctx.emit_match(lseq, lrow, rseq, rrow)
        if ctx.keep_left:
            for i, (lseq, lrow) in enumerate(chunk):
                if not hit[i]:
                    ctx.emit_left_unmatched(lseq, lrow)


def _repartition(ctx, lpart, rpart, depth):
    """Split one partition pair into finer sub-partitions.

    Returns (sub_left, sub_right, progress). ``progress`` is False when
    the right side cannot be shrunk (e.g. all keys hash identically);
    the caller then falls back to block-nested-loop instead of recursing
    forever.
    """
    ctx.stats["repartition_rounds"] += 1
    sub_l = [ctx.new_partition(f"d{depth}L{i}") for i in range(ctx.nparts)]
    sub_r = [ctx.new_partition(f"d{depth}R{i}") for i in range(ctx.nparts)]
    for item in lpart.rows():
        sub_l[_stable_hash(_lkey(ctx, item[1])) % ctx.nparts].add(item)
    for item in rpart.rows():
        sub_r[_stable_hash(_rkey(ctx, item[1])) % ctx.nparts].add(item)
    progress = all(p.count < rpart.count for p in sub_r)
    return sub_l, sub_r, progress


def _join_partition(ctx, lpart, rpart, depth):
    if rpart.count <= ctx.mem:
        _hash_probe(ctx, lpart, rpart)
        return
    if depth < ctx.max_depth:
        sub_l, sub_r, progress = _repartition(ctx, lpart, rpart, depth + 1)
        if progress:
            for sl, sr in zip(sub_l, sub_r):
                if sl.count or sr.count:
                    _join_partition(ctx, sl, sr, depth + 1)
            return
        # No progress: identical/colliding keys. Fall through to BNL.
    _block_nested_loop(ctx, lpart, rpart)


def _normalize_keys(on, schema, side):
    names = [on] if isinstance(on, str) else list(on)
    idx = []
    for name in names:
        for i, (n, _) in enumerate(schema):
            if n == name:
                idx.append(i)
                break
        else:
            raise KeyError(f"{side} join key {name!r} not in schema")
    return idx


def _output_schema(lschema, rschema):
    taken = {n for n, _ in lschema}
    out = list(lschema)
    for name, ct in rschema:
        while name in taken:
            name = name + "_r"
        taken.add(name)
        out.append((name, ct))
    return tuple(out)


def join(left_batches, right_batches, *, left_on, right_on, how="inner",
         num_partitions=16, max_rows_in_memory=10_000, order="left",
         spill_dir=None, max_partition_depth=32) -> JoinResult:
    """Equi-join two streams of RecordBatches.

    left_on / right_on: key column name, or list of names (same length;
    key column types must be identical on both sides).
    how:    "inner" | "left" | "full".
    order:  "left"      -- matched/left rows in left-input order (ties by
                           right-input order); full-join unmatched right
                           rows appended in right-input order. Buffers
                           the output for a final ordering pass.
            "partition" -- streaming-friendly deterministic order:
                           partition 0..N-1, then the non-matchable
                           partition, then (full join) unmatched right.
    """
    if how not in HOWS:
        raise ValueError(f"how must be one of {HOWS}, got {how!r}")
    if order not in ORDERS:
        raise ValueError(f"order must be one of {ORDERS}, got {order!r}")
    if num_partitions < 1:
        raise ValueError("num_partitions must be >= 1")

    left_batches = iter(left_batches)
    right_batches = iter(right_batches)
    try:
        lfirst = next(left_batches)
        rfirst = next(right_batches)
    except StopIteration:
        raise ValueError(
            "both inputs need at least one RecordBatch (it may be empty) "
            "so the schema is known"
        ) from None
    lschema, rschema = lfirst.schema, rfirst.schema

    lkey_idx = _normalize_keys(left_on, lschema, "left")
    rkey_idx = _normalize_keys(right_on, rschema, "right")
    lkt = [lschema[i][1] for i in lkey_idx]
    rkt = [rschema[i][1] for i in rkey_idx]
    if lkt != rkt:
        raise TypeError(f"join key types differ: {lkt} vs {rkt}")

    owns_dir = spill_dir is None
    tmp = tempfile.TemporaryDirectory(prefix="eqjoin_") if owns_dir else None
    workdir = tmp.name if owns_dir else spill_dir
    os.makedirs(workdir, exist_ok=True)

    ctx = _Ctx(how, num_partitions, max_rows_in_memory, order,
               max_partition_depth, workdir, lkey_idx, rkey_idx,
               len(lschema), len(rschema))
    try:
        nonmatch = num_partitions  # index of the NULL/NaN-key partition
        lparts = [ctx.new_partition(f"L{i}") for i in range(num_partitions + 1)]
        rparts = [ctx.new_partition(f"R{i}") for i in range(num_partitions + 1)]

        lseq = 0
        for batch in itertools.chain([lfirst], left_batches):
            if batch.schema != lschema:
                raise ValueError("left batches have inconsistent schemas")
            for row in batch.rows:
                key = _lkey(ctx, row)
                p = nonmatch if not _key_matchable(key) \
                    else _stable_hash(key) % num_partitions
                lparts[p].add((lseq, row))
                lseq += 1
        rseq = 0
        for batch in itertools.chain([rfirst], right_batches):
            if batch.schema != rschema:
                raise ValueError("right batches have inconsistent schemas")
            for row in batch.rows:
                key = _rkey(ctx, row)
                p = nonmatch if not _key_matchable(key) \
                    else _stable_hash(key) % num_partitions
                rparts[p].add((rseq, row))
                rseq += 1

        for p in range(num_partitions):
            if lparts[p].count or rparts[p].count:
                _join_partition(ctx, lparts[p], rparts[p], depth=0)

        if ctx.keep_left:  # NULL/NaN-keyed left rows never match
            for lseq2, lrow in lparts[nonmatch].rows():
                ctx.emit_left_unmatched(lseq2, lrow)

        if how == "full":  # right rows that never matched, incl. NULL keys
            for p in range(num_partitions + 1):
                for rseq2, rrow in rparts[p].rows():
                    if rseq2 not in ctx.matched_right:
                        ctx.emit_right_unmatched(rseq2, rrow)

        ctx.stats["spilled_partitions"] = sum(
            1 for p in ctx.partitions if p.spilled
        )
        if order == "left":
            ctx.out.sort(key=lambda item: item[0])
        rows = [row for _, row in ctx.out]
        return JoinResult(_output_schema(lschema, rschema), rows, ctx.stats)
    finally:
        for part in ctx.partitions:
            part.dispose()
        if tmp is not None:
            tmp.cleanup()
