"""Orchestration: build the constraint system, run branch-and-bound, and
assemble the certified analysis result."""
from __future__ import annotations

from .estimate import independence_estimate
from .model import (
    Join,
    Select,
    canonical_key,
    eval_pred,
    parse_query,
    parse_stats,
    query_tables,
)
from .propagate import Constraint, find_iis
from .search import JoinObjective, Searcher, SelectObjective, problem_fingerprint
from .witness import verify_witness

DEFAULT_CONFIG = {
    "max_tuple_types": 512,    # per-table row-type space must fit this
    "max_nodes": 200_000,      # default node budget for one analyze() call
    "iis_budget": 1_000_000,   # feasibility budget used inside IIS extraction
}


def build_constraints(table, types, offset):
    # One exact-count constraint per statistic, over count variables
    # x[offset + i] = multiplicity of tuple type `types[i]`.
    cons = []
    n = len(types)
    cons.append(
        Constraint(f"{table.name}:row_count", tuple(range(offset, offset + n)),
                   table.row_count)
    )
    for ci, col in enumerate(table.columns):
        for v in col.domain:
            vars_ = tuple(offset + i for i, t in enumerate(types) if t[ci] == v)
            cons.append(
                Constraint(f"{table.name}:hist[{col.name}={v}]", vars_,
                           col.histogram.get(v, 0))
            )
        vars_ = tuple(offset + i for i, t in enumerate(types) if t[ci] is None)
        cons.append(
            Constraint(f"{table.name}:nulls[{col.name}]", vars_, col.null_count)
        )
    ci_map = table.column_index()
    for joint in table.joints:
        ai, bi = ci_map[joint.col_a], ci_map[joint.col_b]
        col_a = table.column(joint.col_a)
        col_b = table.column(joint.col_b)
        for va in list(col_a.domain) + [None]:
            for vb in list(col_b.domain) + [None]:
                vars_ = tuple(
                    offset + i for i, t in enumerate(types)
                    if t[ai] == va and t[bi] == vb
                )
                cons.append(
                    Constraint(
                        f"{table.name}:joint[{joint.col_a}={va},{joint.col_b}={vb}]",
                        vars_, joint.counts.get((va, vb), 0),
                    )
                )
    return cons


def build_objective(query, tables, types_by_table, offsets):
    if isinstance(query, Select):
        table = tables[query.table]
        ci = table.column_index()
        for col in _pred_columns(query.where):
            if col not in ci:
                raise ValueError(f"query references unknown column {col!r}")
        types = types_by_table[query.table]
        coeff = [
            1 if eval_pred(query.where, ci, t) is True else 0 for t in types
        ]
        return SelectObjective(coeff)
    # join
    key_vars = {}
    for side, tname, where, key_col in (
        ("left", query.left_table, query.left_where, query.left_key),
        ("right", query.right_table, query.right_where, query.right_key),
    ):
        table = tables[tname]
        ci = table.column_index()
        for col in _pred_columns(where):
            if col not in ci:
                raise ValueError(f"query references unknown column {col!r}")
        if key_col not in ci:
            raise ValueError(f"join key {key_col!r} is not a column of {tname!r}")
        ki = ci[key_col]
        off = offsets[tname]
        for i, t in enumerate(types_by_table[tname]):
            if eval_pred(where, ci, t) is True and t[ki] is not None:
                entry = key_vars.setdefault(t[ki], [[], []])
                entry[0 if side == "left" else 1].append(off + i)
    merged = [
        (k, lv, rv)
        for k, (lv, rv) in key_vars.items()
        if lv and rv
    ]
    return JoinObjective(merged)


def _pred_columns(pred):
    op = pred[0]
    if op == "true":
        return ()
    if op in ("eq", "range"):
        return (pred[1],)
    if op == "not":
        return _pred_columns(pred[1])
    cols = []
    for sub in pred[1]:
        cols.extend(_pred_columns(sub))
    return tuple(cols)


def _witness(assignment, types_by_table, offsets, involved, claim):
    tbls = {}
    for name in involved:
        types = types_by_table[name]
        off = offsets[name]
        rows = []
        for i, t in enumerate(types):
            rows.extend([list(t)] * assignment[off + i])
        tbls[name] = {"rows": rows}
    return {"tables": tbls, "claim": claim}


def analyze(stats_dict, query_dict, config=None, budget=None, state=None):
    """Run the full certified analysis.

    Returns a JSON-able dict.  When statistics are inconsistent, returns
    {"consistent": False, "conflict": [...]} where `conflict` is a
    deletion-minimal subset of statistics that is still inconsistent.
    Otherwise returns proven [lower, upper] intervals for the true minimum
    and maximum query cardinality, concrete witnesses, a labelled point
    estimate, and search counters.  When the node budget interrupts the
    search, "exact" is False and "state" carries a resumable snapshot.
    """
    cfg = dict(DEFAULT_CONFIG)
    cfg.update(config or {})
    tables = parse_stats(stats_dict)
    query = parse_query(query_dict)
    involved = query_tables(query)
    for name in involved:
        if name not in tables:
            raise ValueError(f"query references unknown table {name!r}")

    types_by_table = {}
    offsets = {}
    constraints = []
    off = 0
    for name in involved:
        types = tables[name].tuple_types()
        if len(types) > cfg["max_tuple_types"]:
            raise ValueError(
                f"table {name!r} has {len(types)} tuple types, above the "
                f"configured limit {cfg['max_tuple_types']}; exact search "
                "is only certified for small domains"
            )
        types_by_table[name] = types
        offsets[name] = off
        constraints.extend(build_constraints(tables[name], types, off))
        off += len(types)
    n_vars = off
    objective = build_objective(query, tables, types_by_table, offsets)
    fingerprint = problem_fingerprint(constraints, objective)

    if state is not None:
        searcher = Searcher.from_json(state, constraints, n_vars, objective)
    else:
        searcher = Searcher(constraints, n_vars, objective, fingerprint)
    budget = budget if budget is not None else cfg["max_nodes"]
    searcher.run(budget)
    lo, hi, exact = searcher.proven_bounds()

    search_stats = {
        "nodes": searcher.nodes,
        "leaves": searcher.leaves,
        "pruned_infeasible": searcher.pruned_infeasible,
        "pruned_bound": searcher.pruned_bound,
        "budget": budget,
        "frontier_remaining": len(searcher.frontier),
    }
    base = {
        "stats_key": canonical_key(stats_dict),
        "query_key": canonical_key(query_dict),
        "search": search_stats,
    }

    if exact and searcher.best_min is None:
        conflict = find_iis(constraints, n_vars, cfg["iis_budget"])
        base.update({
            "consistent": False,
            "conflict": conflict,
            "note": "statistics are mutually inconsistent; 'conflict' is a "
                    "deletion-minimal subset: removing any one member makes "
                    "the remainder consistent",
        })
        return base
    if not exact and searcher.best_min is None:
        base.update({
            "consistent": None,
            "reason": "budget exhausted before any feasible instance was found",
            "state": searcher.to_json(),
        })
        return base

    result = dict(base)
    result.update({
        "consistent": True,
        "exact": exact,
        "min": {
            "lower": lo,
            "upper": searcher.best_min,
            "witness": _witness(searcher.witness_min, types_by_table, offsets,
                                involved, searcher.best_min),
        },
        "max": {
            "lower": searcher.best_max,
            "upper": hi,
            "witness": _witness(searcher.witness_max, types_by_table, offsets,
                                involved, searcher.best_max),
        },
        "estimate": independence_estimate(tables, query),
    })
    if not exact:
        result["state"] = searcher.to_json()
        result["note"] = (
            "budget exhausted: bounds are proven over ALL branches (explored "
            "and unexplored) but are not known to be tight; do not read them "
            "as the exact answer"
        )
    return result


class Analyzer:
    """Stateful facade that never reuses stale proofs.

    Results are cached only under the exact (statistics, query) fingerprint.
    Any change to a statistic produces a new fingerprint, forcing a full
    recomputation; witnesses from earlier statistics are re-validated against
    the new statistics with the independent witness verifier.
    """

    def __init__(self):
        self._cache = {}
        self._history = []

    def analyze(self, stats_dict, query_dict, **kwargs):
        key = canonical_key({"stats": stats_dict, "query": query_dict})
        if key in self._cache:
            cached = dict(self._cache[key])
            cached["cache"] = "hit (identical statistics and query; proof still valid)"
            return cached
        result = analyze(stats_dict, query_dict, **kwargs)
        result["cache"] = "computed"
        result["bounds_reused"] = False
        revalidated = []
        for prev in self._history:
            if prev["stats_key"] == result["stats_key"]:
                continue
            for side in ("min", "max"):
                witness = prev["result"][side]["witness"] if prev["result"].get("consistent") else None
                if witness is None:
                    continue
                verdict = verify_witness(stats_dict, prev["query_dict"], witness)
                revalidated.append({
                    "previous_stats_key": prev["stats_key"],
                    "side": side,
                    "still_valid_under_new_statistics": verdict["ok"],
                    "errors": verdict["errors"],
                })
        result["previous_witnesses_revalidated"] = revalidated
        self._cache[key] = result
        self._history.append({
            "stats_key": result["stats_key"],
            "query_dict": query_dict,
            "result": result,
        })
        return result
