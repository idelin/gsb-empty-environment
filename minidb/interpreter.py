"""Independent reference implementation: naive full-table scan with
three-valued predicate evaluation.  Used by the acceptance tests to
cross-check compiled plans."""
from __future__ import annotations

from .predicates import eval_pred


def interpret(db, version, pred, params=None):
    params = params or {}
    out = []
    for rid in sorted(db.rows):
        vals = db.get(rid, version)
        if vals is None:
            continue
        if eval_pred(pred, vals, params) is True:
            out.append((rid, vals))
    return out
