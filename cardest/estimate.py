"""Heuristic point estimates -- explicitly NOT mathematical bounds.

These use textbook independence / uniformity assumptions on top of the
histograms. They are labeled so they can never be confused with the
proven bounds produced by the search. No confidence interval computed
here is a mathematical bound either.
"""
from __future__ import annotations


def _histogram(spec, rel, col):
    for st in spec["statistics"]:
        if st["type"] == "histogram" and st["relation"] == rel and st["column"] == col:
            return {int(k): v for k, v in st["counts"].items()}
    return None


def _row_count(spec, rel):
    for st in spec["statistics"]:
        if st["type"] == "row_count" and st["relation"] == rel:
            return st["count"]
    return None


def _selectivity(spec, rel, pred):
    """Estimated selectivity of a predicate on one relation, or None."""
    rows = _row_count(spec, rel)
    if rows is None or rows == 0:
        return None
    op = pred["op"]
    if op == "and":
        sels = [_selectivity(spec, rel, a) for a in pred["args"]]
        if any(s is None for s in sels):
            return None
        out = 1.0
        for s in sels:  # independence assumption
            out *= s
        return out
    if op == "or":
        sels = [_selectivity(spec, rel, a) for a in pred["args"]]
        if any(s is None for s in sels):
            return None
        out = 0.0
        for s in sels:  # inclusion-exclusion under independence
            out = out + s - out * s
        return out
    if op == "not":
        s = _selectivity(spec, rel, pred["arg"])
        return None if s is None else 1.0 - s
    col = pred["column"]
    hist = _histogram(spec, rel, col)
    if hist is None:
        return None
    if op == "eq":
        v = pred["value"]
        if v is None:
            return 0.0  # '= NULL' is never true
        return hist.get(v, 0) / rows
    if op == "range":
        low = pred.get("low")
        high = pred.get("high")
        hit = sum(
            c
            for v, c in hist.items()
            if (low is None or v >= low) and (high is None or v <= high)
        )
        return hit / rows
    return None


def _split_predicates(spec, pred):
    """Split a WHERE clause into per-relation conjunctive selectivities."""
    query = spec["query"]
    left, right = query["left"], query["right"]

    def side_of(p):
        if p["op"] in ("and", "or"):
            sides = {side_of(a) for a in p["args"]}
            return sides.pop() if len(sides) == 1 else "both"
        if p["op"] == "not":
            return side_of(p["arg"])
        return p["column"].split(".", 1)[0]

    def strip(p, rel):
        def rewrite(node):
            if node["op"] in ("and", "or"):
                return {"op": node["op"], "args": [rewrite(a) for a in node["args"]]}
            if node["op"] == "not":
                return {"op": "not", "arg": rewrite(node["arg"])}
            return {**node, "column": node["column"].split(".", 1)[1]}

        return rewrite(p)

    sels = {}
    if pred is None:
        return sels
    conjuncts = pred["args"] if pred["op"] == "and" else [pred]
    for c in conjuncts:
        side = side_of(c)
        if side == "both":
            return {}  # cross-relation predicate: give up on decomposition
        s = _selectivity(spec, side, strip(c, side))
        if s is None:
            return {}
        sels[side] = sels.get(side, 1.0) * s
    return sels


def point_estimate(spec):
    """Return a labeled heuristic point estimate of the query row count."""
    query = spec["query"]
    label = {
        "kind": "heuristic_point_estimate",
        "is_proven_bound": False,
        "warning": (
            "point estimate under independence/uniformity assumptions; "
            "NOT a mathematical bound; any empirical confidence interval "
            "around it is not a mathematical bound either"
        ),
    }
    if query["type"] == "select":
        rel = query["from"]
        rows = _row_count(spec, rel)
        where = query.get("where")
        sel = 1.0 if where is None else _selectivity(spec, rel, where)
        if rows is None or sel is None:
            label.update({"value": None, "reason": "insufficient statistics"})
            return label
        label.update(
            {
                "value": rows * sel,
                "assumptions": ["column independence for AND", "histogram uniformity"],
            }
        )
        return label
    # join: estimate per-key via histograms (exact for the bare join),
    # then scale by per-side filter selectivities (independence).
    left, right = query["left"], query["right"]
    on = query["on"]
    if len(on) != 1:
        label.update({"value": None, "reason": "multi-key join estimate unsupported"})
        return label
    lcol, rcol = on[0]
    lhist = _histogram(spec, left, lcol)
    rhist = _histogram(spec, right, rcol)
    if lhist is None or rhist is None:
        label.update({"value": None, "reason": "missing join-key histogram"})
        return label
    base = sum(c * rhist.get(v, 0) for v, c in lhist.items())
    sels = _split_predicates(spec, query.get("where"))
    scale = 1.0
    for s in sels.values():
        scale *= s
    label.update(
        {
            "value": base * scale,
            "assumptions": [
                "join-key frequency from histograms",
                "filter independence across relations",
            ],
        }
    )
    return label
