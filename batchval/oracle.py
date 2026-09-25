"""Independent whole-table structural checker (test oracle).

Deliberately *not* streaming: it materializes the full table in three
phases (structural checks -> flatten rows -> global checks) and is written
independently of Validator. Its only shared piece with the production code
is the column-type rule, which is part of the specification.
"""
from __future__ import annotations

import math

from .model import CType, Severity
from .validator import _type_ok


def _null_of(col, r):
    if col.validity is None:
        return False
    byte = r >> 3
    if byte >= len(col.validity):
        return False
    return not ((col.validity[byte] >> (r & 7)) & 1)


def _key(ctype, is_null, value):
    if is_null:
        return (0, None)
    if ctype is CType.FLOAT64 and isinstance(value, float) and math.isnan(value):
        return (2, None)
    return (1, value)


def check(schema, batches, dictionaries=None, buffers=None) -> list[tuple]:
    """Return expected errors as (kind, severity, column, batch, row)."""
    dictionaries = dictionaries or {}
    buffers = buffers or {}
    errors: list[tuple] = []

    # ---- phase 1: batch structure, storage resolution, dict registration
    resolved_batches = []
    for bidx, batch in enumerate(batches):
        cols = batch.columns
        if len(cols) != len(schema.columns):
            errors.append(("schema", Severity.CORRUPT, None, bidx, None))
        for i, col in enumerate(cols):
            if i < len(schema.columns):
                en, et = schema.columns[i]
                if col.name != en or col.ctype is not et:
                    errors.append(("schema", Severity.CORRUPT, col.name, bidx, None))

        resolved = []
        for col in cols:
            vals = None
            if col.buffer is not None:
                ref = col.buffer
                buf = buffers.get(ref.buffer_id)
                if buf is None:
                    errors.append(("buffer", Severity.MISSING, col.name, bidx, None))
                elif ref.offset < 0 or ref.length < 0 or ref.offset > len(buf) \
                        or ref.offset + ref.length > len(buf):
                    errors.append(("buffer", Severity.CORRUPT, col.name, bidx, None))
                    off = max(0, min(ref.offset, len(buf)))
                    ln = max(0, min(ref.length, len(buf) - off))
                    vals = list(buf[off:off + ln])
                else:
                    vals = list(buf[ref.offset:ref.offset + ref.length])
            elif col.data is not None:
                vals = list(col.data)
            else:
                errors.append(("buffer", Severity.MISSING, col.name, bidx, None))
            resolved.append(vals)

        if batch.row_count is None:
            errors.append(("length", Severity.MISSING, None, bidx, None))
            n = max((len(v) for v in resolved if v is not None), default=0)
        elif batch.row_count < 0:
            errors.append(("length", Severity.CORRUPT, None, bidx, None))
            n = 0
        else:
            n = batch.row_count

        for col, vals in zip(cols, resolved):
            if vals is not None and len(vals) != n:
                errors.append(("length", Severity.CORRUPT, col.name, bidx, None))

        for col, vals in zip(cols, resolved):
            if col.validity is not None:
                ncol = len(vals) if vals is not None else n
                need = (ncol + 7) // 8
                if len(col.validity) != need:
                    errors.append(("bitmap", Severity.CORRUPT, col.name, bidx, None))

        for i, (col, vals) in enumerate(zip(cols, resolved)):
            if vals is not None and col.dict_id is not None:
                versions = dictionaries.get(col.dict_id) or {}
                if versions.get(col.dict_version) is None:
                    name = schema.columns[i][0] if i < len(schema.columns) else col.name
                    errors.append(("dict_version", Severity.MISSING, name, bidx, None))

        resolved_batches.append((n, resolved))

    # ---- phase 2: flatten every column position into global row order
    ncols = max((len(b.columns) for b in batches), default=0)
    sortable = [[] for _ in range(ncols)]   # entries that may feed sort checks
    row_base = 0
    type_seen: set = set()
    dict_seen: set = set()
    for bidx, batch in enumerate(batches):
        n, resolved = resolved_batches[bidx]
        for i, vals in enumerate(resolved):
            col = batch.columns[i]
            name, etype = (schema.columns[i] if i < len(schema.columns)
                           else (col.name, col.ctype))
            versions = dictionaries.get(col.dict_id) or {}
            entries = versions.get(col.dict_version) if col.dict_id is not None else None
            for r, v in enumerate(vals or ()):
                grow = row_base + r
                is_null = _null_of(col, r)
                if is_null:
                    sortable[i].append((bidx, grow, True, None, name, etype))
                    continue
                if col.dict_id is not None:
                    if not (isinstance(v, int) and not isinstance(v, bool)):
                        if (bidx, i) not in type_seen:
                            type_seen.add((bidx, i))
                            errors.append(("type", Severity.CORRUPT, name, bidx, grow))
                        continue
                    if entries is None:
                        continue
                    if not (0 <= v < len(entries)):
                        if (bidx, i) not in dict_seen:
                            dict_seen.add((bidx, i))
                            errors.append(("dict_index", Severity.CORRUPT, name, bidx, grow))
                        continue
                    decoded = entries[v]
                    if not _type_ok(etype, decoded):
                        if (bidx, i) not in type_seen:
                            type_seen.add((bidx, i))
                            errors.append(("type", Severity.CORRUPT, name, bidx, grow))
                        continue
                    sortable[i].append((bidx, grow, False, decoded, name, etype))
                else:
                    if not _type_ok(etype, v):
                        if (bidx, i) not in type_seen:
                            type_seen.add((bidx, i))
                            errors.append(("type", Severity.CORRUPT, name, bidx, grow))
                        continue
                    sortable[i].append((bidx, grow, False, v, name, etype))
        row_base += n

    # ---- phase 3: global running-extreme ordering check
    state: dict[str, list] = {}
    for i in range(ncols):
        for bidx, grow, is_null, value, name, etype in sortable[i]:
            sdir = schema.sorted.get(name)
            if not sdir:
                continue
            key = _key(etype, is_null, value)
            st = state.get(name)
            if st is None:
                state[name] = [key, grow]
                continue
            inverted = key < st[0] if sdir == "asc" else key > st[0]
            if inverted:
                errors.append(("order", Severity.CORRUPT, name, bidx, grow))
            else:
                st[0], st[1] = key, grow

    return errors
