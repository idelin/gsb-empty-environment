"""Independent brute-force reference enumerator.

Cross-checks the production branch-and-bound searcher on small instances.
This module is deliberately self-contained: it does NOT import the
production searcher, constraint compiler, or query evaluator, so the two
implementations can only agree if both are correct.
"""
from __future__ import annotations

from itertools import product


def _cells_for(spec, rel):
    cols = list(spec["relations"][rel]["columns"].keys())
    domains = [sorted(spec["relations"][rel]["columns"][c]["domain"]) + [None] for c in cols]
    return cols, [dict(zip(cols, t)) for t in product(*domains)]


def _constraints_for(spec, relations):
    """Own compilation of statistics to (cell_index_list, target) pairs."""
    per_rel = {rel: _cells_for(spec, rel) for rel in relations}
    constraints = []
    for st in spec["statistics"]:
        rel = st["relation"]
        if rel not in per_rel:
            continue
        cols, cells = per_rel[rel]
        if st["type"] == "row_count":
            constraints.append((rel, list(range(len(cells))), st["count"]))
        elif st["type"] == "histogram":
            col = st["column"]
            counts = {int(k): v for k, v in st["counts"].items()}
            domain = spec["relations"][rel]["columns"][col]["domain"]
            for v in domain:
                idx = [i for i, cell in enumerate(cells) if cell[col] == v]
                constraints.append((rel, idx, counts.get(v, 0)))
        elif st["type"] == "null_count":
            col = st["column"]
            idx = [i for i, cell in enumerate(cells) if cell[col] is None]
            constraints.append((rel, idx, st["count"]))
        elif st["type"] == "joint_count":
            jcols = st["columns"]
            given = {tuple(e["values"]): e["count"] for e in st["counts"]}
            domains = [spec["relations"][rel]["columns"][c]["domain"] for c in jcols]
            combos = [tuple(vs) for vs in product(*[sorted(d) for d in domains])]
            combos += [k for k in given if any(v is None for v in k)]
            for combo in combos:
                idx = [
                    i
                    for i, cell in enumerate(cells)
                    if all(cell[c] == v for c, v in zip(jcols, combo))
                ]
                constraints.append((rel, idx, given.get(combo, 0)))
    return per_rel, constraints


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
    raise ValueError(op)


def _query_count(spec, assign):
    """assign: {rel: [multiplicity per cell]}."""
    query = spec["query"]
    where = query.get("where")
    if query["type"] == "select":
        rel = query["from"]
        cols, cells = _cells_for(spec, rel)
        total = 0
        for i, cell in enumerate(cells):
            if where is None or _eval(where, lambda c, r=cell: r[c]) is True:
                total += assign[rel][i]
        return total
    left, right = query["left"], query["right"]
    _, lcells = _cells_for(spec, left)
    _, rcells = _cells_for(spec, right)
    on = query["on"]
    total = 0
    for li, lrow in enumerate(lcells):
        lkey = tuple(lrow[c] for c, _ in on)
        if any(v is None for v in lkey):
            continue
        lm = assign[left][li]
        if not lm:
            continue
        for ri, rrow in enumerate(rcells):
            rm = assign[right][ri]
            if not rm:
                continue
            if tuple(rrow[c] for _, c in on) != lkey:
                continue
            if where is None:
                total += lm * rm
            else:
                def get(ref, lr=lrow, rr=rrow):
                    r, c = ref.split(".", 1)
                    return lr[c] if r == left else rr[c]

                if _eval(where, get) is True:
                    total += lm * rm
    return total


def enumerate_exact(spec, max_nodes=2_000_000):
    """Enumerate every statistic-consistent assignment; return exact min/max.

    Returns dict with min, max, witnesses (assignments), nodes explored.
    Raises RuntimeError if the safety cap is exceeded.
    """
    query = spec["query"]
    if query["type"] == "select":
        relations = [query["from"]]
    else:
        relations = [query["left"], query["right"]]
    per_rel, constraints = _constraints_for(spec, relations)
    # flat cell list: (rel, local_index)
    flat = [(rel, i) for rel in relations for i in range(len(per_rel[rel][1]))]
    cons_by_cell = [[] for _ in flat]
    flat_cons = []
    for rel, idxs, target in constraints:
        # compute offset of rel in flat list
        offset = 0
        for r in relations:
            if r == rel:
                break
            offset += len(per_rel[r][1])
        cells = [offset + i for i in idxs]
        ci = len(flat_cons)
        flat_cons.append((cells, target))
        for c in cells:
            cons_by_cell[c].append(ci)

    best = {"min": None, "max": None, "min_w": None, "max_w": None, "nodes": 0}
    assign = [0] * len(flat)
    remaining = [t for _, t in flat_cons]

    def rec(pos):
        if best["nodes"] >= max_nodes:
            raise RuntimeError("reference enumerator safety cap exceeded")
        if pos == len(flat):
            if any(r != 0 for r in remaining):
                return
            best["nodes"] += 1
            grouped = {}
            offset = 0
            for rel in relations:
                n = len(per_rel[rel][1])
                grouped[rel] = assign[offset : offset + n]
                offset += n
            value = _query_count(spec, grouped)
            if best["min"] is None or value < best["min"]:
                best["min"] = value
                best["min_w"] = list(assign)
            if best["max"] is None or value > best["max"]:
                best["max"] = value
                best["max_w"] = list(assign)
            return
        # simple remaining-sum pruning only (no propagation)
        ub = None
        for ci in cons_by_cell[pos]:
            ub = remaining[ci] if ub is None else min(ub, remaining[ci])
        if ub is None:
            ub = 0  # unconstrained cells: 0 is optimal-harmless
        for v in range(ub + 1):
            assign[pos] = v
            for ci in cons_by_cell[pos]:
                remaining[ci] -= v
            rec(pos + 1)
            for ci in cons_by_cell[pos]:
                remaining[ci] += v
        assign[pos] = 0

    rec(0)
    if best["min"] is None:
        return {"consistent": False, "nodes": best["nodes"]}
    return {
        "consistent": True,
        "min": best["min"],
        "max": best["max"],
        "min_witness": best["min_w"],
        "max_witness": best["max_w"],
        "nodes": best["nodes"],
    }
