"""Witness verifier: independently re-checks a claimed relation instance.

A witness is a concrete multiset of rows per table.  Verification recomputes
*every* statistic from the rows and compares it against the declared
statistics, then evaluates the query directly (SQL three-valued logic,
duplicate-preserving join multiplicity).  It never calls the searcher.
"""
from __future__ import annotations

from .model import parse_query, parse_stats, query_count, query_tables


def _check_rows(table, rows, errors):
    ncols = len(table.columns)
    for rno, row in enumerate(rows):
        if len(row) != ncols:
            errors.append(
                f"{table.name}: row {rno} has arity {len(row)}, expected {ncols}"
            )
            return
        for ci, col in enumerate(table.columns):
            v = row[ci]
            if v is not None and v not in col.domain:
                errors.append(
                    f"{table.name}: row {rno} column {col.name}: "
                    f"value {v!r} outside domain"
                )


def _check_stats(table, rows, errors):
    if len(rows) != table.row_count:
        errors.append(
            f"{table.name}: row_count is {table.row_count}, witness has {len(rows)}"
        )
    for ci, col in enumerate(table.columns):
        for v in col.domain:
            got = sum(1 for r in rows if r[ci] == v)
            want = col.histogram.get(v, 0)
            if got != want:
                errors.append(
                    f"{table.name}: histogram[{col.name}={v}] is {want}, "
                    f"witness has {got}"
                )
        got_null = sum(1 for r in rows if r[ci] is None)
        if got_null != col.null_count:
            errors.append(
                f"{table.name}: null_count[{col.name}] is {col.null_count}, "
                f"witness has {got_null}"
            )
    ci_map = table.column_index()
    for joint in table.joints:
        ai, bi = ci_map[joint.col_a], ci_map[joint.col_b]
        col_a = table.column(joint.col_a)
        col_b = table.column(joint.col_b)
        for va in list(col_a.domain) + [None]:
            for vb in list(col_b.domain) + [None]:
                got = sum(1 for r in rows if r[ai] == va and r[bi] == vb)
                want = joint.counts.get((va, vb), 0)
                if got != want:
                    errors.append(
                        f"{table.name}: joint[{joint.col_a}={va},"
                        f"{joint.col_b}={vb}] is {want}, witness has {got}"
                    )


def verify_witness(stats_dict, query_dict, witness):
    """Verify a witness against statistics and query.

    Returns {"ok", "errors", "query_count", "claimed"}; `ok` requires every
    statistic to match and (if a claim is present) the claimed query
    cardinality to equal the recomputed one.
    """
    tables = parse_stats(stats_dict)
    query = parse_query(query_dict)
    errors = []
    rows_by_table = {}
    wt = witness.get("tables", {}) if isinstance(witness, dict) else {}
    for name in query_tables(query):
        entry = wt.get(name)
        if entry is None:
            errors.append(f"witness is missing table {name!r}")
            continue
        rows = [tuple(r) for r in entry.get("rows", [])]
        rows_by_table[name] = rows
        _check_rows(tables[name], rows, errors)
        _check_stats(tables[name], rows, errors)
    count = None
    if not errors:
        count = query_count(tables, query, rows_by_table)
    claimed = witness.get("claim") if isinstance(witness, dict) else None
    if claimed is not None and count is not None and claimed != count:
        errors.append(f"claimed query count {claimed}, recomputed {count}")
    return {
        "ok": not errors,
        "errors": errors,
        "query_count": count,
        "claimed": claimed,
    }
