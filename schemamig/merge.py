"""Three-way merge of branch schemas derived from a common base.

Columns are matched by identity (cid), never by name.  Rules:
  - same identity renamed along different chains must converge to the
    same final name, otherwise the merge is a conflict;
  - a new column may never swallow an old column (or another new column)
    by reusing its name: distinct identities with equal names conflict;
  - drop vs modify is a conflict; drop vs untouched drops the column;
  - type changes must agree, or one side must be an exact widening of
    the other.

The result carries the merged schema plus the residual migrations from
each branch tip to the merged schema, so callers can check that the two
composed migration paths (base->A->merge and base->B->merge) are
value-equivalent.
"""
from __future__ import annotations

from dataclasses import dataclass

from .migration import AddColumn, Migration, RenameColumn, WidenColumn
from .schema import Column, Schema
from .typesys import is_exact_widening


class MergeConflict(Exception):
    pass


@dataclass
class MergeResult:
    schema: Schema
    from_a: Migration
    from_b: Migration


def _merge_name(base_col, ca, cb):
    if ca.name == cb.name:
        return ca.name
    a_changed = ca.name != base_col.name
    b_changed = cb.name != base_col.name
    if a_changed and b_changed:
        raise MergeConflict(
            f"column #{base_col.cid} renamed to {ca.name!r} and {cb.name!r}: "
            f"rename chains do not converge")
    return ca.name if a_changed else cb.name


def _merge_type(base_col, ca, cb):
    if ca.type is cb.type:
        return ca.type
    a_changed = ca.type is not base_col.type
    b_changed = cb.type is not base_col.type
    if a_changed and b_changed:
        raise MergeConflict(
            f"column #{base_col.cid}: conflicting types "
            f"{ca.type.value} vs {cb.type.value}")
    wider = ca.type if a_changed else cb.type
    other = cb.type if a_changed else ca.type
    if not is_exact_widening(other, wider):
        raise MergeConflict(
            f"column #{base_col.cid}: {other.value}->{wider.value} "
            f"is not an exact widening")
    return wider


def three_way_merge(base: Schema, mig_a: Migration, mig_b: Migration) -> MergeResult:
    sa = mig_a.apply(base).schema
    sb = mig_b.apply(base).schema
    state_a = {c.cid: c for c in sa.columns}
    state_b = {c.cid: c for c in sb.columns}

    merged = []
    ops_a, ops_b = [], []

    for base_col in base.columns:
        ca = state_a.get(base_col.cid)
        cb = state_b.get(base_col.cid)
        if ca is None and cb is None:
            continue
        if ca is None or cb is None:
            survivor = ca if ca is not None else cb
            if survivor != base_col:
                raise MergeConflict(
                    f"column #{base_col.cid} modified in one branch but "
                    f"dropped in the other")
            continue
        name = _merge_name(base_col, ca, cb)
        typ = _merge_type(base_col, ca, cb)
        if ca.nullable != cb.nullable:
            raise MergeConflict(
                f"column #{base_col.cid}: nullability differs between branches")
        merged.append(Column(base_col.cid, name, typ, ca.nullable))
        if ca.name != name:
            ops_a.append(RenameColumn(ca.cid, name))
        if cb.name != name:
            ops_b.append(RenameColumn(cb.cid, name))
        if ca.type is not typ:
            ops_a.append(WidenColumn(ca.cid, typ))
        if cb.type is not typ:
            ops_b.append(WidenColumn(cb.cid, typ))

    added = {}
    for state in (state_a, state_b):
        for cid, col in state.items():
            if base.has(cid):
                continue
            if cid in added:
                if added[cid] != col:
                    raise MergeConflict(
                        f"column identity {cid!r} added with different "
                        f"definitions by the two branches")
                continue
            added[cid] = col

    taken = {col.name: col.cid for col in merged}
    for cid, col in added.items():
        if col.name in taken:
            raise MergeConflict(
                f"new column {col.name!r}#{cid} collides with existing "
                f"column #{taken[col.name]}: a new column must not swallow "
                f"another column identity")
        taken[col.name] = cid

    for cid, col in added.items():
        merged.append(col)
        if cid not in state_a:
            ops_a.append(AddColumn(cid, col.name, col.type, col.nullable))
        if cid not in state_b:
            ops_b.append(AddColumn(cid, col.name, col.type, col.nullable))

    return MergeResult(Schema(merged), Migration(ops_a), Migration(ops_b))
