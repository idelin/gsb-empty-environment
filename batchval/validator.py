"""Streaming combined validator.

Checks, per batch and across the batch stream:
  schema match, column length, validity bitmap, shared buffer references,
  global dictionary versions + dictionary indices, value types (exact int64
  range), and declared sort order carried across batch boundaries.

The validator never materializes the table: per-batch columns are iterated
row by row and long-lived state is only (a) the running sort extreme per
declared column and (b) the accumulated errors. Out-of-range offsets/indices
are clamped so a single bad offset can never cause an unbounded read or
abort the remaining independent checks.
"""
from __future__ import annotations

import math

from .model import Batch, CType, Error, Severity, TableSchema

_INT64_MIN = -(2 ** 63)
_INT64_MAX = 2 ** 63 - 1


def _is_null(col, r: int) -> bool:
    if col.validity is None:
        return False
    byte = r >> 3
    if byte >= len(col.validity):       # short bitmap already reported; do not over-read
        return False
    return not ((col.validity[byte] >> (r & 7)) & 1)


def _sort_key(ctype: CType, is_null: bool, value):
    # Total order per column: NULL < numbers/strings/booleans < NaN.
    if is_null:
        return (0, None)
    if ctype is CType.FLOAT64 and isinstance(value, float) and math.isnan(value):
        return (2, None)
    return (1, value)


def _type_ok(ctype: CType, v) -> bool:
    if ctype is CType.INT64:
        return isinstance(v, int) and not isinstance(v, bool) and _INT64_MIN <= v <= _INT64_MAX
    if ctype is CType.FLOAT64:
        return isinstance(v, (int, float)) and not isinstance(v, bool)
    if ctype is CType.UTF8:
        return isinstance(v, str)
    if ctype is CType.BOOL:
        return isinstance(v, bool)
    return False


def _display(is_null: bool, value) -> str:
    if is_null:
        return "NULL"
    if isinstance(value, float) and math.isnan(value):
        return "NaN"
    return repr(value)


def earliest_bad_row(errors) -> int | None:
    rows = [e.row for e in errors if e.row is not None and e.severity is Severity.CORRUPT]
    return min(rows) if rows else None


class Validator:
    def __init__(self, schema: TableSchema, dictionaries=None, buffers=None):
        self.schema = schema
        self.dictionaries = dictionaries or {}   # dict_id -> {version: [entries]}
        self.buffers = buffers or {}             # buffer_id -> sequence
        self._errors: list[Error] = []
        self._row_base = 0
        self._batch_index = 0
        self._sort_state: dict[str, list] = {}   # name -> [key, global_row, display]

    def _err(self, kind, severity, column, row, message, evidence=()):
        self._errors.append(
            Error(kind, severity, column, self._batch_index, row, message, tuple(evidence)))

    # ------------------------------------------------------------------ feed
    def feed(self, batch: Batch) -> "Validator":
        base = self._row_base
        cols = batch.columns

        # 1. schema: column count and per-column name/type
        if len(cols) != len(self.schema.columns):
            self._err("schema", Severity.CORRUPT, None, None,
                      f"batch has {len(cols)} columns, schema declares {len(self.schema.columns)}",
                      (len(self.schema.columns), len(cols)))
        for i, col in enumerate(cols):
            if i < len(self.schema.columns):
                exp_name, exp_type = self.schema.columns[i]
                if col.name != exp_name or col.ctype is not exp_type:
                    self._err("schema", Severity.CORRUPT, col.name, None,
                              f"column {i}: expected {exp_name} ({exp_type.value}), "
                              f"got {col.name} ({col.ctype.value})")

        # 2. resolve storage, clamping any out-of-range shared-buffer reference
        resolved = [self._resolve(col) for col in cols]

        # 3. declared row count (missing data is not corruption)
        if batch.row_count is None:
            self._err("length", Severity.MISSING, None, None, "batch row_count not declared")
            n = max((len(v) for v in resolved if v is not None), default=0)
        elif batch.row_count < 0:
            self._err("length", Severity.CORRUPT, None, None,
                      f"negative row_count {batch.row_count}")
            n = 0
        else:
            n = batch.row_count

        # 4. per-column length
        for col, vals in zip(cols, resolved):
            if vals is not None and len(vals) != n:
                self._err("length", Severity.CORRUPT, col.name, None,
                          f"column has {len(vals)} values, expected {n}", (n, len(vals)))

        # 5. validity bitmap size (exactly ceil(rows/8) bytes)
        for col, vals in zip(cols, resolved):
            if col.validity is not None:
                ncol = len(vals) if vals is not None else n
                need = (ncol + 7) // 8
                if len(col.validity) != need:
                    self._err("bitmap", Severity.CORRUPT, col.name, None,
                              f"validity bitmap has {len(col.validity)} bytes, expected {need}",
                              (need, len(col.validity)))

        # 6. row-level checks streamed in column order
        for i, col in enumerate(cols):
            self._check_column_rows(i, col, resolved[i], base)

        self._row_base += n
        self._batch_index += 1
        return self

    def _resolve(self, col):
        """Resolve a column's value list; clamp bad buffer references."""
        if col.buffer is not None:
            ref = col.buffer
            buf = self.buffers.get(ref.buffer_id)
            if buf is None:
                self._err("buffer", Severity.MISSING, col.name, None,
                          f"shared buffer {ref.buffer_id!r} not provided", (ref.buffer_id,))
                return None
            if ref.offset < 0 or ref.length < 0 or ref.offset > len(buf) \
                    or ref.offset + ref.length > len(buf):
                self._err("buffer", Severity.CORRUPT, col.name, None,
                          f"buffer reference [{ref.offset}:{ref.offset + ref.length}] out of "
                          f"bounds for buffer of size {len(buf)}; read clamped",
                          (len(buf), ref.offset, ref.length))
                off = max(0, min(ref.offset, len(buf)))
                ln = max(0, min(ref.length, len(buf) - off))
                return list(buf[off:off + ln])
            return list(buf[ref.offset:ref.offset + ref.length])
        if col.data is not None:
            return col.data
        self._err("buffer", Severity.MISSING, col.name, None,
                  "column has neither inline data nor a buffer reference")
        return None

    # --------------------------------------------------------- row-level work
    def _check_column_rows(self, i: int, col, vals, base: int):
        if vals is None:
            return
        if i < len(self.schema.columns):
            name, etype = self.schema.columns[i]
        else:
            name, etype = col.name, col.ctype
        sdir = self.schema.sorted.get(name)

        entries = None
        if col.dict_id is not None:
            versions = self.dictionaries.get(col.dict_id) or {}
            entries = versions.get(col.dict_version)
            if entries is None:
                self._err("dict_version", Severity.MISSING, name, None,
                          f"dictionary {col.dict_id!r} version {col.dict_version} not registered",
                          (col.dict_id, col.dict_version))

        type_reported = False
        dict_reported = False
        for r, v in enumerate(vals):
            grow = base + r
            if _is_null(col, r):
                if sdir:  # NULL participates in ordering; value checks are skipped
                    self._check_order(name, sdir, etype, True, None, grow)
                continue

            if col.dict_id is not None:
                if not (isinstance(v, int) and not isinstance(v, bool)):
                    if not type_reported:
                        type_reported = True
                        self._err("type", Severity.CORRUPT, name, grow,
                                  f"dictionary index must be int, got {type(v).__name__}")
                    continue
                if entries is None:
                    continue  # missing version already reported; cannot decode
                if not (0 <= v < len(entries)):
                    if not dict_reported:
                        dict_reported = True
                        self._err("dict_index", Severity.CORRUPT, name, grow,
                                  f"dictionary index {v} out of range [0, {len(entries)})",
                                  (v, len(entries)))
                    continue
                decoded = entries[v]
                if not _type_ok(etype, decoded):
                    if not type_reported:
                        type_reported = True
                        self._err("type", Severity.CORRUPT, name, grow,
                                  f"dictionary entry {decoded!r} does not match {etype.value}")
                    continue
                if sdir:
                    self._check_order(name, sdir, etype, False, decoded, grow)
            else:
                if not _type_ok(etype, v):
                    if not type_reported:
                        type_reported = True
                        self._err("type", Severity.CORRUPT, name, grow,
                                  f"value {v!r} does not match column type {etype.value}")
                    continue
                if sdir:
                    self._check_order(name, sdir, etype, False, v, grow)

    def _check_order(self, name, sdir, ctype, is_null, value, grow):
        key = _sort_key(ctype, is_null, value)
        state = self._sort_state.get(name)
        if state is None:
            self._sort_state[name] = [key, grow, _display(is_null, value)]
            return
        best_key, best_row, best_disp = state
        inverted = key < best_key if sdir == "asc" else key > best_key
        if inverted:
            self._err("order", Severity.CORRUPT, name, grow,
                      f"{sdir} order violated between rows {best_row} and {grow}",
                      ((best_row, best_disp), (grow, _display(is_null, value))))
            # keep the running extreme so later independent inversions are still found
        else:
            state[0], state[1], state[2] = key, grow, _display(is_null, value)

    # ------------------------------------------------ pause / resume / output
    def snapshot(self) -> dict:
        return {
            "row_base": self._row_base,
            "batch_index": self._batch_index,
            "sort_state": {k: list(v) for k, v in self._sort_state.items()},
            "errors": list(self._errors),
        }

    @classmethod
    def restore(cls, schema, snap: dict, dictionaries=None, buffers=None) -> "Validator":
        v = cls(schema, dictionaries, buffers)
        v._row_base = snap["row_base"]
        v._batch_index = snap["batch_index"]
        v._sort_state = {k: list(val) for k, val in snap["sort_state"].items()}
        v._errors = list(snap["errors"])
        return v

    def errors(self) -> list[Error]:
        return list(self._errors)


def validate(schema: TableSchema, batches, dictionaries=None, buffers=None) -> list[Error]:
    """Validate any iterable of batches lazily (generators accepted)."""
    v = Validator(schema, dictionaries, buffers)
    for batch in batches:
        v.feed(batch)
    return v.errors()
