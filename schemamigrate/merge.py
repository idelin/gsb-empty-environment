"""Three-way merge of branch schemas against a common base.

Matching is by column identity, never by name:
  - the same identity renamed along different chains must converge to one
    name, otherwise the merge is a conflict;
  - a brand-new column that happens to reuse a name never swallows the
    identity (or data) of an old column;
  - drop vs. modify on the other branch is a conflict; drop vs. untouched
    drops the column.
"""
from __future__ import annotations

from dataclasses import replace

from .schema import Schema, SchemaError


class MergeConflict(Exception):
    pass


def _pick(base_value, left_value, right_value, what, column_name):
    if left_value == right_value:
        return left_value
    if left_value == base_value:
        return right_value
    if right_value == base_value:
        return left_value
    raise MergeConflict(
        f"column {column_name!r}: {what} changed divergently "
        f"(base={base_value!r}, left={left_value!r}, right={right_value!r})"
    )


def merge_schemas(base: Schema, left: Schema, right: Schema) -> Schema:
    left_by_id = {c.column_id: c for c in left.columns}
    right_by_id = {c.column_id: c for c in right.columns}
    merged = []
    for b in base.columns:
        l = left_by_id.get(b.column_id)
        r = right_by_id.get(b.column_id)
        if l is None and r is None:
            continue
        if l is None or r is None:
            kept = l if l is not None else r
            if kept == b:
                continue  # dropped on one branch, untouched on the other
            raise MergeConflict(
                f"column {b.name!r}: dropped on one branch but modified on the other"
            )
        name = _pick(b.name, l.name, r.name, "name", b.name)
        type_ = _pick(b.type, l.type, r.type, "type", b.name)
        nullable = _pick(b.nullable, l.nullable, r.nullable, "nullability", b.name)
        merged.append(replace(b, name=name, type=type_, nullable=nullable))
    base_ids = {c.column_id for c in base.columns}
    seen_added = set()
    for branch, other_by_id in ((left, right_by_id), (right, left_by_id)):
        for col in branch.columns:
            if col.column_id in base_ids or col.column_id in seen_added:
                continue
            twin = other_by_id.get(col.column_id)
            if twin is not None and twin != col:
                raise MergeConflict(
                    f"added column identity {col.column_id!r} differs between branches"
                )
            seen_added.add(col.column_id)
            merged.append(col)
    try:
        return Schema(merged)
    except SchemaError as exc:
        raise MergeConflict(str(exc)) from exc
