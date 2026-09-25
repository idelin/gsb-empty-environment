"""Independent brute-force reference enumerator.

This module deliberately shares NO code with the production searcher
(cardian.search / cardian.propagate / cardian.analyze).  It re-parses the
JSON dictionaries on its own, enumerates every count vector by plain
recursion, checks each statistic by direct summation, and evaluates the
query with its own three-valued logic.  It exists solely to cross-check the
production analyzer on tiny instances.
"""
import itertools


def _val(token):
    if token is None or token == "null":
        return None
    return int(token)


def _parse_tables(stats):
    tables = {}
    for name, raw in stats["tables"].items():
        cols = []
        for cname, craw in raw["columns"].items():
            domain = sorted(int(v) for v in craw["domain"])
            hist = {int(k): int(v) for k, v in craw.get("histogram", {}).items()}
            cols.append((cname, domain, int(craw.get("null_count", 0)), hist))
        joints = []
        for jraw in raw.get("joint_counts", []):
            a, b = jraw["columns"]
            counts = {}
            for k, v in jraw["counts"].items():
                ta, tb = str(k).split(",")
                counts[(_val(ta.strip()), _val(tb.strip()))] = int(v)
            joints.append(((a, b), counts))
        tables[name] = {
            "row_count": int(raw["row_count"]),
            "columns": cols,
            "joints": joints,
        }
    return tables


def _types(table):
    doms = [list(d) + [None] for (_, d, _, _) in table["columns"]]
    return list(itertools.product(*doms))


def _compositions(total, parts):
    if parts == 0:
        if total == 0:
            yield ()
        return
    if parts == 1:
        yield (total,)
        return
    for first in range(total + 1):
        for rest in _compositions(total - first, parts - 1):
            yield (first,) + rest


def _matches(table, types, vec):
    for ci, (_cname, domain, null_count, hist) in enumerate(table["columns"]):
        for v in domain:
            want = hist.get(v, 0)
            got = sum(n for t, n in zip(types, vec) if t[ci] == v)
            if got != want:
                return False
        got_null = sum(n for t, n in zip(types, vec) if t[ci] is None)
        if got_null != null_count:
            return False
    cidx = {c[0]: i for i, c in enumerate(table["columns"])}
    for (a, b), counts in table["joints"]:
        ai, bi = cidx[a], cidx[b]
        dom_a = list(table["columns"][ai][1]) + [None]
        dom_b = list(table["columns"][bi][1]) + [None]
        for va in dom_a:
            for vb in dom_b:
                want = counts.get((va, vb), 0)
                got = sum(
                    n for t, n in zip(types, vec) if t[ai] == va and t[bi] == vb
                )
                if got != want:
                    return False
    return True


def _feasible(table):
    types = _types(table)
    out = []
    for vec in _compositions(table["row_count"], len(types)):
        if _matches(table, types, vec):
            out.append(vec)
    return types, out


def _eval(node, cidx, row):
    if node is None:
        return True
    (op, arg), = node.items()
    if op == "eq":
        col, val = arg
        x = row[cidx[col]]
        if x is None or val is None or val == "null":
            return None
        return x == int(val)
    if op == "range":
        col, lo, hi = arg
        x = row[cidx[col]]
        if x is None or lo in (None, "null") or hi in (None, "null"):
            return None
        return int(lo) <= x <= int(hi)
    if op == "and":
        vals = [_eval(p, cidx, row) for p in arg]
        if False in vals:
            return False
        if None in vals:
            return None
        return True
    if op == "or":
        vals = [_eval(p, cidx, row) for p in arg]
        if True in vals:
            return True
        if None in vals:
            return None
        return False
    if op == "not":
        v = _eval(arg, cidx, row)
        return None if v is None else (not v)
    raise ValueError(f"unknown operator {op!r}")


def _rows(types, vec):
    rows = []
    for t, n in zip(types, vec):
        rows.extend([list(t)] * n)
    return rows


def brute_force(stats, query):
    """Exact min/max query cardinality by total enumeration.

    Returns {"min", "max", "min_rows", "max_rows", "feasible"} where
    min_rows/max_rows are concrete row multisets attaining the bounds.
    """
    tables = _parse_tables(stats)
    best = {"min": None, "max": None, "min_rows": None, "max_rows": None,
            "feasible": 0}

    def consider(value, rows):
        best["feasible"] += 1
        if best["min"] is None or value < best["min"]:
            best["min"] = value
            best["min_rows"] = rows
        if best["max"] is None or value > best["max"]:
            best["max"] = value
            best["max_rows"] = rows

    if "select" in query:
        spec = query["select"]
        name = spec["table"]
        table = tables[name]
        cidx = {c[0]: i for i, c in enumerate(table["columns"])}
        types, vecs = _feasible(table)
        for vec in vecs:
            cnt = sum(
                n for t, n in zip(types, vec)
                if _eval(spec.get("where"), cidx, t) is True
            )
            consider(cnt, {name: {"rows": _rows(types, vec)}})
        return best

    join = query["join"]
    sides = []
    for spec, key_col in ((join["left"], join["on"][0]),
                          (join["right"], join["on"][1])):
        name = spec["table"]
        table = tables[name]
        cidx = {c[0]: i for i, c in enumerate(table["columns"])}
        ki = cidx[key_col]
        types, vecs = _feasible(table)
        keyed = []
        for vec in vecs:
            key_counts = {}
            for t, n in zip(types, vec):
                if n and t[ki] is not None and _eval(spec.get("where"), cidx, t) is True:
                    key_counts[t[ki]] = key_counts.get(t[ki], 0) + n
            keyed.append((vec, key_counts))
        sides.append((name, types, keyed))
    (lname, ltypes, lkeyed), (rname, rtypes, rkeyed) = sides
    for lvec, lk in lkeyed:
        for rvec, rk in rkeyed:
            total = sum(n * rk.get(k, 0) for k, n in lk.items())
            consider(total, {
                lname: {"rows": _rows(ltypes, lvec)},
                rname: {"rows": _rows(rtypes, rvec)},
            })
    return best
