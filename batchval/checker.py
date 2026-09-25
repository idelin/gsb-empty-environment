"""Independent reference checker.

A deliberately naive, fully materializing second implementation of the
same specification. Tests cross-check the streaming validator against
this checker: identical error key sets must be produced. Unlike the
streaming validator this one resolves whole columns up front; it is
only meant as a verification oracle, not for production use.
"""
from __future__ import annotations

import math

from .model import (
    Batch,
    ColType,
    DictRegistry,
    K,
    Schema,
    bitmap_get,
)

ROW_LEVEL = {K.NULL, K.TYPE, K.DICT_INDEX, K.SORT}
NULL = object()  # resolved null marker


def independent_check(schema: Schema, registry: DictRegistry,
                      batches: list[Batch]) -> set[tuple]:
    """Return the set of (kind, column, global_row) errors."""
    errors: set[tuple] = set()
    seen_row_kind: set[tuple] = set()

    def report(kind, column, row):
        if kind in ROW_LEVEL:
            if (kind, column) in seen_row_kind:
                return
            seen_row_kind.add((kind, column))
        errors.add((kind, column, row))

    row_base = 0
    last_key: dict = {}
    sort_done: set = set()
    max_ver: dict = {}

    for batch in batches:
        declared = batch.declared_rows
        if declared < 0:
            report(K.LENGTH, None, None)
        if len(batch.columns) != len(schema.columns):
            report(K.SCHEMA, None, None)

        # shared buffers, by object identity
        groups: dict[int, list[str]] = {}
        for c in batch.columns:
            if c.values is not None:
                groups.setdefault(id(c.values), []).append(c.spec.name)
        declared_shared = {frozenset(v) for v in batch.shared_buffers.values()}
        for names in groups.values():
            if len(names) > 1 and frozenset(names) not in declared_shared:
                report(K.SHARED, None, None)
        for grp in declared_shared:
            objs = {id(c.values) for c in batch.columns
                    if c.spec.name in grp and c.values is not None}
            present = {c.spec.name for c in batch.columns if c.spec.name in grp}
            if present != set(grp) or len(objs) > 1:
                report(K.SHARED, None, None)

        for pos, col in enumerate(batch.columns):
            spec = schema.columns[pos] if pos < len(schema.columns) else col.spec
            name = spec.name
            if pos < len(schema.columns):
                want = schema.columns[pos]
                if (col.spec.name, col.spec.type) != (want.name, want.type):
                    report(K.SCHEMA, col.spec.name, None)

            if col.values is None:
                report(K.BUFFER, name, None)
                continue

            if col.length != len(col.values) or len(col.values) != declared:
                report(K.LENGTH, name, None)
            n = min(len(col.values), max(declared, 0))

            bitmap = col.bitmap
            expected = (max(declared, 0) + 7) // 8
            if spec.nullable:
                if bitmap is None:
                    report(K.BITMAP, name, None)
                elif len(bitmap) != expected:
                    report(K.BITMAP, name, None)
            elif bitmap is not None:
                if len(bitmap) != expected:
                    report(K.BITMAP, name, None)
                for i in range(min(max(declared, 0), len(bitmap) * 8)):
                    if not bitmap_get(bitmap, i):
                        report(K.NULL, name, row_base + i)
                        break

            def valid(i, bm=bitmap):
                if bm is None or i >> 3 >= len(bm):
                    return True
                return bitmap_get(bm, i)

            dictionary = None
            if spec.type is ColType.STR:
                if col.dict_ref is None:
                    report(K.DICT_REF, name, None)
                else:
                    dictionary = registry.get(*col.dict_ref)
                    if dictionary is None:
                        report(K.DICT_REF, name, None)
                    else:
                        _, ver = col.dict_ref
                        seen = max_ver.get(name)
                        if seen is not None and ver < seen:
                            report(K.DICT_STALE, name, None)
                        else:
                            max_ver[name] = ver if seen is None else max(seen, ver)

            # resolve the whole column (naive: full materialization)
            resolved = []
            for i in range(n):
                if not valid(i):
                    resolved.append(NULL)
                    continue
                v = col.values[i]
                if v is None:
                    report(K.NULL, name, row_base + i)
                    resolved.append(NULL)
                    continue
                if spec.type is ColType.INT:
                    if type(v) is not int:
                        report(K.TYPE, name, row_base + i)
                        resolved.append(NULL)
                        continue
                    resolved.append(v)
                elif spec.type is ColType.FLOAT:
                    if isinstance(v, bool) or not isinstance(v, (int, float)):
                        report(K.TYPE, name, row_base + i)
                        resolved.append(NULL)
                        continue
                    resolved.append(v)
                else:
                    if type(v) is not int:
                        report(K.TYPE, name, row_base + i)
                        resolved.append(NULL)
                        continue
                    if v < 0 or (dictionary is not None and v >= len(dictionary.values)):
                        report(K.DICT_INDEX, name, row_base + i)
                        resolved.append(NULL)
                        continue
                    resolved.append(dictionary.values[v] if dictionary else NULL)

            # ordering check on the resolved column
            if spec.sorted_asc and name not in sort_done:
                prev = last_key.get(name, NULL)
                done = False
                for i, key in enumerate(resolved):
                    if key is NULL:
                        continue
                    if spec.type is ColType.FLOAT and isinstance(key, float) and math.isnan(key):
                        report(K.SORT, name, row_base + i)
                        done = True
                        break
                    if prev is not NULL and key < prev:
                        report(K.SORT, name, row_base + i)
                        done = True
                        break
                    prev = key
                if done:
                    sort_done.add(name)
                elif prev is not NULL:
                    last_key[name] = prev

        row_base += max(declared, 0)

    return errors
