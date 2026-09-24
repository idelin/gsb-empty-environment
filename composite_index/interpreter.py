"""Independent full-scan interpreter used as the correctness oracle.

It ignores indexes entirely: every row visible at the snapshot version is
read from the table and the predicate is evaluated with three-valued
logic; only TRUE rows are returned, in row-id order.
"""
from __future__ import annotations

from .predicates import eval3


def full_scan_interpret(db, pred, version):
    out = []
    for rid in sorted(db.table.rows):
        row = db.table.read(rid, version)
        if row is None:
            continue
        if eval3(pred, row) is True:
            out.append((rid, row))
    return out
