"""Independent witness verifier.

Given a spec and a witness (concrete relation instances), re-check every
statistic by direct counting and re-evaluate the query with SQL
three-valued logic. This module deliberately does NOT import the search
engine, so a bug in the searcher cannot be masked by the verifier.
"""
from __future__ import annotations

from .spec import spec_fingerprint


def _expand_rows(witness_rel):
    rows = []
    for entry in witness_rel:
        rows.extend([entry["tuple"]] * entry["count"])
    return rows


def _check_statistic(stat, rows):
    stype = stat["type"]
    if stype == "row_count":
        actual = len(rows)
        return actual == stat["count"], actual
    if stype == "histogram":
        col = stat["column"]
        expected = {int(k): v for k, v in stat["counts"].items()}
        actual = {}
        for row in rows:
            v = row[col]
            if v is not None:
                actual[v] = actual.get(v, 0) + 1
        # histogram is complete over the domain: omitted values mean zero
        ok = all(actual.get(v, 0) == expected.get(v, 0) for v in set(actual) | set(expected))
        return ok, actual
    if stype == "null_count":
        col = stat["column"]
        actual = sum(1 for row in rows if row[col] is None)
        return actual == stat["count"], actual
    if stype == "joint_count":
        cols = stat["columns"]
        expected = {tuple(e["values"]): e["count"] for e in stat["counts"]}
        actual = {}
        for row in rows:
            key = tuple(row[c] for c in cols)
            if all(v is not None for v in key):
                actual[key] = actual.get(key, 0) + 1
        for key, cnt in expected.items():
            if any(v is None for v in key):
                n = sum(1 for row in rows if all(row[c] == v for c, v in zip(cols, key)))
                if n != cnt:
                    return False, {"mismatch": [key, cnt, n]}
        ok = all(actual.get(k, 0) == expected.get(k, 0) for k in set(actual) | set(expected)
                 if all(v is not None for v in k))
        return ok, actual
    return False, f"unknown statistic type {stype!r}"


def _tv_and(vals):
    if any(v is False for v in vals):
        return False
    if any(v is None for v in vals):
        return None
    return True


def _tv_or(vals):
    if any(v is True for v in vals):
        return True
    if any(v is None for v in vals):
        return None
    return False


def _eval(pred, get):
    op = pred["op"]
    if op == "and":
        return _tv_and([_eval(a, get) for a in pred["args"]])
    if op == "or":
        return _tv_or([_eval(a, get) for a in pred["args"]])
    if op == "not":
        v = _eval(pred["arg"], get)
        return None if v is None else not v
    if op == "eq":
        v = get(pred["column"])
        t = pred["value"]
        if v is None or t is None:
            return None
        return v == t
    if op == "range":
        v = get(pred["column"])
        if v is None:
            return None
        if pred.get("low") is not None and v < pred["low"]:
            return False
        if pred.get("high") is not None and v > pred["high"]:
            return False
        return True
    raise ValueError(f"unknown op {op!r}")


def evaluate_query(spec, relations_rows):
    """Count query result rows on concrete relation instances (multisets)."""
    query = spec["query"]
    where = query.get("where")
    if query["type"] == "select":
        rows = relations_rows[query["from"]]
        if where is None:
            return len(rows)
        return sum(1 for row in rows if _eval(where, lambda c, r=row: r[c]) is True)
    left = relations_rows[query["left"]]
    right = relations_rows[query["right"]]
    on = query["on"]
    total = 0
    for lrow in left:
        lkey = tuple(lrow[c] for c, _ in on)
        if any(v is None for v in lkey):
            continue  # NULL keys never match
        for rrow in right:
            if tuple(rrow[c] for _, c in on) != lkey:
                continue
            if where is None:
                total += 1
            else:
                lname, rname = query["left"], query["right"]

                def get(ref, lr=lrow, rr=rrow, ln=lname):
                    rel, col = ref.split(".", 1)
                    return lr[col] if rel == ln else rr[col]

                if _eval(where, get) is True:
                    total += 1
    return total


def verify(spec, witness, claimed_value, expected_fingerprint=None):
    """Verify a witness against all statistics and the claimed query count.

    Returns a dict with 'valid' (bool) and per-check details. If
    expected_fingerprint is given, the spec must hash to it, otherwise the
    certificate is reported STALE (statistics changed after issuance).
    """
    report = {"valid": True, "checks": []}
    fingerprint = spec_fingerprint(spec)
    report["spec_fingerprint"] = fingerprint
    if expected_fingerprint is not None and expected_fingerprint != fingerprint:
        report["valid"] = False
        report["stale"] = True
        report["detail"] = (
            "specification changed since the certificate was issued; "
            "old witnesses and bounds must be re-derived"
        )
        return report
    relations_rows = {}
    for rel, entries in witness.items():
        relations_rows[rel] = _expand_rows(entries)
    # every statistic must hold on the witness
    for stat in spec["statistics"]:
        rel = stat["relation"]
        if rel not in relations_rows:
            report["valid"] = False
            report["checks"].append({"id": stat["id"], "ok": False, "detail": "relation missing"})
            continue
        ok, actual = _check_statistic(stat, relations_rows[rel])
        report["checks"].append({"id": stat["id"], "ok": ok, "actual": actual})
        if not ok:
            report["valid"] = False
    # the query count on the witness must equal the claimed value
    actual_count = evaluate_query(spec, relations_rows)
    report["query_count"] = actual_count
    report["claimed_value"] = claimed_value
    if actual_count != claimed_value:
        report["valid"] = False
    return report
