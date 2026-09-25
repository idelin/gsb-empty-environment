"""Streaming validator.

Two layers:

* ``validate_batch_structure`` -- pure per-batch structural checks
  (schema, length, bitmap, dictionary indices, shared buffers). Every
  column is checked independently: one bad offset can neither trigger an
  unbounded read nor mask errors in other columns.

* ``StreamValidator`` -- incremental cross-batch checks (declared
  ordering, global dictionary versions). It keeps only O(#columns) state
  (last sort key per ordered column, max dictionary version seen, dedup
  sets), so arbitrarily long streams are validated without materializing
  them. ``snapshot()`` / the ``state=`` constructor argument give
  pause/resume; feeding the same rows in any chunking yields the same
  error set.

Missing data (absent buffer, absent bitmap, unknown dictionary version)
is reported with severity MISSING and only limits the checks that depend
on it; everything else is still validated.
"""
from __future__ import annotations

import math

from .model import (
    ROW_LEVEL_KINDS,
    Batch,
    ColType,
    DictRegistry,
    Error,
    K,
    Schema,
    Severity,
    bitmap_get,
)

_WINDOW = 2  # evidence radius: at most 2*WINDOW+1 items per slice
_SENTINEL = object()


def _window(seq, i: int, radius: int = _WINDOW) -> tuple:
    """Minimal evidence slice around index i, clamped to the buffer."""
    lo = max(0, i - radius)
    hi = min(len(seq), i + radius + 1)
    return (lo, tuple(seq[lo:hi]))


def _is_valid(col, i: int) -> bool:
    """Null test: bitmap bit 0 means null. A short bitmap fails open
    (already reported as BITMAP_INVALID) so it cannot mask value errors."""
    bm = col.bitmap
    if bm is None:
        return True
    if i >> 3 >= len(bm):
        return True
    return bitmap_get(bm, i)


class _ColumnErrors:
    """Keeps only the earliest-row error per kind within one column."""

    def __init__(self) -> None:
        self._first: dict[str, Error] = {}
        self._count: dict[str, int] = {}

    def add(self, err: Error) -> None:
        if err.kind in self._first:
            self._count[err.kind] += 1
        else:
            self._first[err.kind] = err
            self._count[err.kind] = 1

    def errors(self) -> list[Error]:
        return [e.with_occurrences(self._count[k]) for k, e in self._first.items()]


def _validate_column(batch, col, spec, registry, row_base, out) -> None:
    bid = batch.batch_id
    name = spec.name
    declared = batch.declared_rows

    if col.values is None:
        out.append(Error(K.BUFFER, Severity.MISSING, bid, None, name,
                         f"value buffer for column {name!r} is absent"))
        return

    if col.length != len(col.values) or len(col.values) != declared:
        out.append(Error(
            K.LENGTH, Severity.CORRUPT, bid, None, name,
            f"column {name!r}: header length={col.length}, "
            f"buffer rows={len(col.values)}, batch rows={declared}"))

    # All row loops are bounded by the buffers actually present.
    n = min(len(col.values), max(declared, 0))

    bitmap = col.bitmap
    expected_bytes = (max(declared, 0) + 7) // 8
    if spec.nullable:
        if bitmap is None:
            out.append(Error(K.BITMAP, Severity.MISSING, bid, None, name,
                             f"validity bitmap for nullable column {name!r} is absent"))
        elif len(bitmap) != expected_bytes:
            out.append(Error(K.BITMAP, Severity.CORRUPT, bid, None, name,
                             f"bitmap has {len(bitmap)} bytes, expected {expected_bytes}"))
    elif bitmap is not None:
        if len(bitmap) != expected_bytes:
            out.append(Error(K.BITMAP, Severity.CORRUPT, bid, None, name,
                             f"bitmap has {len(bitmap)} bytes, expected {expected_bytes}"))
        limit = min(max(declared, 0), len(bitmap) * 8)
        for i in range(limit):
            if not bitmap_get(bitmap, i):
                out.append(Error(K.NULL, Severity.CORRUPT, bid, row_base + i, name,
                                 f"null bit in non-nullable column {name!r}",
                                 evidence=_window(col.values, min(i, max(len(col.values) - 1, 0)))))
                break

    dictionary = None
    dict_size = None
    if spec.type is ColType.STR:
        if col.dict_ref is None:
            out.append(Error(K.DICT_REF, Severity.MISSING, bid, None, name,
                             f"no dictionary reference for column {name!r}"))
        else:
            dictionary = registry.get(*col.dict_ref)
            if dictionary is None:
                out.append(Error(K.DICT_REF, Severity.MISSING, bid, None, name,
                                 f"dictionary {col.dict_ref} is not registered"))
            else:
                dict_size = len(dictionary.values)

    row_errs = _ColumnErrors()
    for i in range(n):
        if not _is_valid(col, i):
            continue  # null slot: value is meaningless, NaN included
        v = col.values[i]
        grow = row_base + i
        if v is None:
            row_errs.add(Error(K.NULL, Severity.CORRUPT, bid, grow, name,
                               "valid bit set but value slot is null",
                               evidence=_window(col.values, i)))
            continue
        if spec.type is ColType.INT:
            if type(v) is not int:  # bool rejected: not an exact integer
                row_errs.add(Error(K.TYPE, Severity.CORRUPT, bid, grow, name,
                                   f"expected exact int, got {type(v).__name__}",
                                   evidence=_window(col.values, i)))
        elif spec.type is ColType.FLOAT:
            if isinstance(v, bool) or not isinstance(v, (int, float)):
                row_errs.add(Error(K.TYPE, Severity.CORRUPT, bid, grow, name,
                                   f"expected float/int, got {type(v).__name__}",
                                   evidence=_window(col.values, i)))
            # NaN here is a legitimate value, distinct from null: no error.
        elif spec.type is ColType.STR:
            if type(v) is not int:
                row_errs.add(Error(K.TYPE, Severity.CORRUPT, bid, grow, name,
                                   f"expected dictionary index (int), got {type(v).__name__}",
                                   evidence=_window(col.values, i)))
            elif v < 0 or (dict_size is not None and v >= dict_size):
                # Bounds checked BEFORE any dictionary access: a corrupt
                # offset can never cause an unbounded read.
                row_errs.add(Error(K.DICT_INDEX, Severity.CORRUPT, bid, grow, name,
                                   f"dictionary index {v} out of range "
                                   f"(dictionary size {dict_size})",
                                   evidence=_window(col.values, i)))
    out.extend(row_errs.errors())


def _check_shared_buffers(batch, out) -> None:
    by_id: dict[int, list[str]] = {}
    for col in batch.columns:
        if col.values is not None:
            by_id.setdefault(id(col.values), []).append(col.spec.name)
    declared = {frozenset(cols) for cols in batch.shared_buffers.values()}
    for names in by_id.values():
        if len(names) > 1 and frozenset(names) not in declared:
            out.append(Error(K.SHARED, Severity.CORRUPT, batch.batch_id, None, None,
                             f"columns {sorted(names)} share one buffer "
                             f"without a shared-buffer declaration"))
    for group in declared:
        present = {c.spec.name for c in batch.columns if c.spec.name in group}
        objs = {id(c.values) for c in batch.columns
                if c.spec.name in group and c.values is not None}
        if present != set(group) or len(objs) > 1:
            out.append(Error(K.SHARED, Severity.CORRUPT, batch.batch_id, None, None,
                             f"declared shared buffer {sorted(group)} is not "
                             f"actually shared by those columns"))


def validate_batch_structure(batch, schema, registry, row_base: int = 0) -> list[Error]:
    out: list[Error] = []
    if batch.declared_rows < 0:
        out.append(Error(K.LENGTH, Severity.CORRUPT, batch.batch_id, None, None,
                         f"negative declared_rows={batch.declared_rows}"))
    if len(batch.columns) != len(schema.columns):
        out.append(Error(K.SCHEMA, Severity.CORRUPT, batch.batch_id, None, None,
                         f"batch has {len(batch.columns)} columns, "
                         f"schema has {len(schema.columns)}"))
    for pos, col in enumerate(batch.columns):
        if pos < len(schema.columns):
            want = schema.columns[pos]
            if (col.spec.name, col.spec.type) != (want.name, want.type):
                out.append(Error(K.SCHEMA, Severity.CORRUPT, batch.batch_id, None,
                                 col.spec.name,
                                 f"position {pos}: expected {want.name}:"
                                 f"{want.type.value}, got {col.spec.name}:"
                                 f"{col.spec.type.value}"))
        spec = schema.columns[pos] if pos < len(schema.columns) else col.spec
        _validate_column(batch, col, spec, registry, row_base, out)
    _check_shared_buffers(batch, out)
    return out


class StreamValidator:
    """Incremental validator with O(#columns) cross-batch state."""

    def __init__(self, schema: Schema, registry: DictRegistry, state: dict | None = None):
        self.schema = schema
        self.registry = registry
        st = state or {}
        self._row_base: int = st.get("row_base", 0)
        self._last_key: dict = dict(st.get("last_key", {}))
        self._sort_done: set = set(st.get("sort_done", ()))
        self._max_dict_ver: dict = dict(st.get("max_dict_ver", {}))
        self._reported: set = set(st.get("reported", ()))

    def snapshot(self) -> dict:
        """Picklable pause/resume state (constant size)."""
        return {
            "row_base": self._row_base,
            "last_key": dict(self._last_key),
            "sort_done": set(self._sort_done),
            "max_dict_ver": dict(self._max_dict_ver),
            "reported": set(self._reported),
        }

    def feed_batch(self, batch: Batch) -> list[Error]:
        raw = validate_batch_structure(batch, self.schema, self.registry, self._row_base)
        errors = []
        for e in raw:
            if e.kind in ROW_LEVEL_KINDS:
                key = (e.kind, e.column)
                if key in self._reported:
                    continue
                self._reported.add(key)
            errors.append(e)
        errors.extend(self._check_dict_versions(batch))
        errors.extend(self._check_sort(batch))
        self._row_base += max(batch.declared_rows, 0)
        return errors

    def _check_dict_versions(self, batch: Batch) -> list[Error]:
        out = []
        for pos, col in enumerate(batch.columns):
            spec = self.schema.columns[pos] if pos < len(self.schema.columns) else col.spec
            if spec.type is not ColType.STR or col.dict_ref is None:
                continue
            _, version = col.dict_ref
            if self.registry.get(*col.dict_ref) is None:
                continue  # already reported as MISSING by structural checks
            seen = self._max_dict_ver.get(spec.name)
            if seen is not None and version < seen:
                out.append(Error(K.DICT_STALE, Severity.CORRUPT, batch.batch_id, None,
                                 spec.name,
                                 f"dictionary version {version} used after "
                                 f"version {seen} in the same stream"))
            else:
                self._max_dict_ver[spec.name] = version if seen is None else max(seen, version)
        return out

    def _check_sort(self, batch: Batch) -> list[Error]:
        out = []
        for pos, spec in enumerate(self.schema.columns):
            if not spec.sorted_asc or spec.name in self._sort_done:
                continue  # undeclared columns are never treated as ordered
            if pos >= len(batch.columns):
                continue
            col = batch.columns[pos]
            if col.values is None:
                continue
            dictionary = None
            if spec.type is ColType.STR and col.dict_ref is not None:
                dictionary = self.registry.get(*col.dict_ref)
            n = min(len(col.values), max(batch.declared_rows, 0))
            prev = self._last_key.get(spec.name, _SENTINEL)
            violation = None
            for i in range(n):
                if not _is_valid(col, i):
                    continue  # nulls do not participate in ordering
                v = col.values[i]
                if v is None:
                    continue
                if spec.type is ColType.STR:
                    if (type(v) is not int or dictionary is None
                            or v < 0 or v >= len(dictionary.values)):
                        continue  # already reported structurally
                    key = dictionary.values[v]
                elif spec.type is ColType.FLOAT:
                    if isinstance(v, bool) or not isinstance(v, (int, float)):
                        continue
                    if isinstance(v, float) and math.isnan(v):
                        violation = (i, "NaN in column declared ordered "
                                        "(NaN is a value, not a null)", (prev if prev is not _SENTINEL else None, "NaN"))
                        break
                    key = v
                else:
                    if type(v) is not int:
                        continue
                    key = v  # exact integer comparison, no float coercion
                if prev is not _SENTINEL and key < prev:
                    violation = (i, f"order inversion: {key!r} < previous key {prev!r}",
                                 (prev, key))
                    break
                prev = key
            if violation is not None:
                i, msg, evidence = violation
                out.append(Error(K.SORT, Severity.CORRUPT, batch.batch_id,
                                 self._row_base + i, spec.name, msg,
                                 evidence=evidence))
                self._sort_done.add(spec.name)  # earliest bad row only
            elif prev is not _SENTINEL:
                self._last_key[spec.name] = prev
        return out
